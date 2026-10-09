package updater

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// uniqueSorted：去除空白与重复项并排序（网络名/别名的恢复顺序依赖它）。
func TestUniqueSortedTrimsDedupesAndSorts(t *testing.T) {
	got := uniqueSorted([]string{" b", "a", "b ", "", "  ", "c", "a"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got=%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got=%v want=%v", got, want)
		}
	}
	if got := uniqueSorted(nil); len(got) != 0 {
		t.Fatalf("nil 输入应得空切片：%v", got)
	}
}

// 挂载身份用于比较前后两次 inspect，挂载参数用于 docker run -v：
// volume 走命名卷，bind 的“身份”必须规范化（Clean）、而参数保持原样。
func TestMountIdentityAndDockerMountArg(t *testing.T) {
	volume := dataMount{Type: "volume", Name: "agent2api-data", Source: "/var/lib/docker/volumes/agent2api-data/_data"}
	if got := mountIdentity(volume); got != "volume:agent2api-data" {
		t.Fatalf("identity=%q", got)
	}
	if got := dockerMountArg(volume); got != "agent2api-data:/data" {
		t.Fatalf("arg=%q", got)
	}

	bind := dataMount{Type: "bind", Source: "/srv/agent2api/./data/../data"}
	if got := mountIdentity(bind); got != "bind:/srv/agent2api/data" {
		t.Fatalf("identity=%q（bind 必须 Clean 后比较）", got)
	}
	if got := dockerMountArg(bind); got != "/srv/agent2api/./data/../data:/data" {
		t.Fatalf("arg=%q（bind 参数必须原样保留）", got)
	}
}

func containerFromJSON(t *testing.T, raw string) containerInspect {
	t.Helper()
	var list []containerInspect
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("fixture len=%d", len(list))
	}
	return list[0]
}

// /data 挂载发现：命名卷与 bind 都要识别；未挂 /data 或类型不符必须拒绝
// （否则回滚会对着一个不存在的持久化点做备份/恢复）。
func TestFindDataMountVariants(t *testing.T) {
	volume := containerFromJSON(t, `[{"Image":"sha256:x","Mounts":[{"Type":"volume","Name":"agent2api-data","Source":"/v/_data","Destination":"/data"}]}]`)
	mount, err := findDataMount(volume)
	if err != nil || mount.Type != "volume" || mount.Name != "agent2api-data" {
		t.Fatalf("volume=(%+v, %v)", mount, err)
	}

	bind := containerFromJSON(t, `[{"Image":"x","Mounts":[{"Type":"bind","Source":"/srv/data","Destination":"/data"}]}]`)
	mount, err = findDataMount(bind)
	if err != nil || mount.Type != "bind" || mount.Source != "/srv/data" || mount.Name != "" {
		t.Fatalf("bind=(%+v, %v)", mount, err)
	}

	none := containerFromJSON(t, `[{"Image":"x","Mounts":[]}]`)
	if _, err := findDataMount(none); err == nil || !strings.Contains(err.Error(), "no persistent /data mount") {
		t.Fatalf("无挂载 err=%v", err)
	}

	wrongType := containerFromJSON(t, `[{"Image":"x","Mounts":[{"Type":"tmpfs","Name":"","Source":"","Destination":"/data"}]}]`)
	if _, err := findDataMount(wrongType); err == nil {
		t.Fatal("tmpfs /data 必须被拒绝")
	}

	otherDest := containerFromJSON(t, `[{"Image":"x","Mounts":[{"Type":"volume","Name":"other","Destination":"/other"}]}]`)
	if _, err := findDataMount(otherDest); err == nil {
		t.Fatal("非 /data 挂载必须被拒绝")
	}
}

// inspectContainer：单容器 + Image 非空才算有效；0/多容器、空 Image、
// 坏 JSON、命令失败都要以可读错误返回。
func TestInspectContainerViaRunner(t *testing.T) {
	executor := NewExecutor(ExecutorConfig{})
	runner := &scriptedRunner{t: t, steps: []runnerStep{
		{name: "docker", args: []string{"inspect", "agent2api"}, output: []byte(`[{"Image":"sha256:abc"}]`)},
	}}
	executor.runner = runner
	got, err := executor.inspectContainer(context.Background())
	if err != nil || got.Image != "sha256:abc" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	runner.assertDone()

	cases := []struct {
		name    string
		output  []byte
		runErr  error
		wantErr string
	}{
		{"空数组", []byte(`[]`), nil, "not found"},
		{"空 Image", []byte(`[{"Image":""}]`), nil, "not found"},
		{"多容器", []byte(`[{"Image":"a"},{"Image":"b"}]`), nil, "not found"},
		{"坏 JSON", []byte(`{`), nil, "decode container inspect"},
		{"命令失败", nil, errors.New("docker exploded"), "docker exploded"},
	}
	for _, tc := range cases {
		exec := NewExecutor(ExecutorConfig{})
		exec.runner = &scriptedRunner{t: t, steps: []runnerStep{
			{name: "docker", args: []string{"inspect", "agent2api"}, output: tc.output, err: tc.runErr},
		}}
		if _, err := exec.inspectContainer(context.Background()); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("%s: err=%v", tc.name, err)
		}
	}
}

// env 文件读写：读要带出权限位；写必须原子替换且 mode=0 落 0600；
// 目录缺失不得留半成品。
func TestReadAndWriteEnvFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("A=1\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	data, mode, err := readEnvFile(path)
	if err != nil || string(data) != "A=1\n" || mode.Perm() != 0o640 {
		t.Fatalf("read=(%q, %v, %v)", data, mode, err)
	}
	if _, _, err := readEnvFile(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("缺失文件必须报错")
	}

	if err := writeEnvFileAtomic(path, 0, []byte("NEW=1\n")); err != nil {
		t.Fatal(err)
	}
	data, mode, err = readEnvFile(path)
	if err != nil || string(data) != "NEW=1\n" || mode.Perm() != 0o600 {
		t.Fatalf("mode=0 应落 0600：(%q, %v, %v)", data, mode, err)
	}

	if err := writeEnvFileAtomic(filepath.Join(dir, "nodir", ".env"), 0o600, []byte("x")); err == nil {
		t.Fatal("缺失目录必须报错")
	}
}

// setEnvValueAtomic：CRLF 归一、缺键追加（末尾空行规整）。
func TestSetEnvValueAtomicAppendsMissingKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("A=1\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := setEnvValueAtomic(path, 0o600, "B", "2"); err != nil {
		t.Fatal(err)
	}
	data, _, err := readEnvFile(path)
	if err != nil || string(data) != "A=1\nB=2\n" {
		t.Fatalf("data=%q err=%v", data, err)
	}
}

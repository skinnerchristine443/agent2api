package control

import (
	"context"
	"encoding/json"
	"testing"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
)

// 批量导入会独立报告每一项：imported / skipped（重复凭据，无论是已存储的
// 还是批内自身重复的）/ error（无法解码的项）。单项失败不得阻断其他项。
func TestImportBatchReportsPerItemAndSkipsDuplicates(t *testing.T) {
	log := &callLog{}
	store := &fakeStore{
		log: log,
		accounts: map[string]accounts.Account{
			"existing": {ID: "existing", Name: "old", Provider: "workbuddy", ProviderRegion: "cn"},
		},
		payloads:       map[string][]byte{"existing": []byte(`{"t":"2"}`)},
		payloadFormats: map[string]string{"existing": "workbuddy-oauth-v1"},
	}
	service := NewAccounts(&fakeRuntime{log: log, store: store})
	service.Providers = providers.NewRegistry()
	service.Providers.Register(providers.Adapter{ID: "workbuddy", Credential: &credentialStub{format: "workbuddy-oauth-v1"}})

	items := []json.RawMessage{
		json.RawMessage(`{"format":"workbuddy-oauth-v1","name":"A","provider":"workbuddy","credential":{"t":"1"}}`),
		json.RawMessage(`{"format":"workbuddy-oauth-v1","name":"B","provider":"workbuddy","credential":{"t":"1"}}`),
		json.RawMessage(`{"format":"workbuddy-oauth-v1","name":"C","provider":"workbuddy","credential":{"t":"2"}}`),
		json.RawMessage(`not json`),
	}
	results := service.ImportBatch(context.Background(), items)
	if len(results) != len(items) {
		t.Fatalf("results = %d, want %d", len(results), len(items))
	}
	want := []string{"imported", "skipped", "skipped", "error"}
	for index, item := range results {
		if item.Index != index || item.Status != want[index] {
			t.Fatalf("item %d = %+v, want status %s", index, item, want[index])
		}
	}
	if results[0].AccountID == "" || results[0].Name != "A" {
		t.Fatalf("imported item must carry the account: %+v", results[0])
	}
	if results[1].Error == "" || results[2].Error == "" || results[3].Error == "" {
		t.Fatalf("skipped/error items must carry a reason: %+v", results)
	}
}

package updater

import (
	"testing"
	"time"

	"agent2api/internal/store"
)

// store 负责写快照名；updater 在应用升级前校验它。若两者不一致，
// 每次升级都会在 apply 步骤被拒（"invalid sqlite backup path"）——
// 因此在这里钉住该契约。
func TestBackupNamePatternAcceptsWriterOutput(t *testing.T) {
	name := store.BackupName(time.Now())
	if !backupNamePattern.MatchString(name) {
		t.Fatalf("snapshot writer produced %q, which backupNamePattern rejects", name)
	}
}

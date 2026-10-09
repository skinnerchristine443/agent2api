package control

import (
	accountruntime "agent2api/internal/runtime"
)

var _ Runtime = (*accountruntime.Manager)(nil)

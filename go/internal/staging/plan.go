// Chunk planning: a pure function of the manifest and the chunk size.
package staging

import (
	"fmt"

	"bulkwire/internal/model"
)

// Plan splits each group (in manifest order) into ceil(rowCount/chunkSize) chunks. Every chunk has chunkSize
// rows except the group's last, so a 251-row group with chunkSize 250 yields chunks of 250 and 1. A chunk
// never spans two groups, so a group-level policy decision applies cleanly to whole chunks.
//
// Planning needs only the manifest (group offsets and counts), never the rows. That is why PlanChunks is cheap
// and why its result, a few dozen small descriptors, is safe to keep in workflow history.
func Plan(m model.Manifest, chunkSize int) ([]model.ChunkRef, error) {
	if chunkSize <= 0 {
		return nil, fmt.Errorf("staging: chunk size must be positive, got %d", chunkSize)
	}
	chunks := []model.ChunkRef{}
	for gi, g := range m.Groups {
		for ci, off := 0, 0; off < g.RowCount; ci, off = ci+1, off+chunkSize {
			chunks = append(chunks, model.ChunkRef{
				ChunkID:  ChunkID(gi, ci),
				GroupKey: g.GroupKey,
				Offset:   g.StartOffset + off,
				Length:   min(chunkSize, g.RowCount-off),
			})
		}
	}
	return chunks, nil
}

// ChunkID is "g{groupIndex:02d}-c{chunkIndex:02d}", e.g. "g03-c01".
func ChunkID(groupIndex, chunkIndex int) string {
	return fmt.Sprintf("g%02d-c%02d", groupIndex, chunkIndex)
}

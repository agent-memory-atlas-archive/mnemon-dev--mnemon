package search

import (
	"github.com/mnemon-dev/mnemon/internal/memory/embed"
	"github.com/mnemon-dev/mnemon/internal/memory/model"
	"github.com/mnemon-dev/mnemon/internal/memory/store"
)

// recallCache belongs to one synchronous recall. It is discarded afterwards so
// updates, deletions and changed query vectors cannot leave stale cached state.
type recallCache struct {
	db           *store.DB
	insights     map[string]*model.Insight
	edges        map[string][]*model.Edge
	similarities map[string]float64
}

func newRecallCache(db *store.DB, all []*model.Insight, queryVec []float64) *recallCache {
	c := &recallCache{
		db: db, insights: make(map[string]*model.Insight, len(all)),
		edges: make(map[string][]*model.Edge),
	}
	for _, ins := range all {
		c.insights[ins.ID] = ins
	}
	if queryVec != nil {
		c.similarities = loadQuerySimilarities(db, queryVec)
	}
	return c
}

func (c *recallCache) edgesFor(id string) ([]*model.Edge, error) {
	if edges, ok := c.edges[id]; ok {
		return edges, nil
	}
	edges, err := c.db.GetEdgesByNode(id)
	if err == nil {
		// Retain empty adjacency lists too, but let failed reads be retried.
		c.edges[id] = edges
	}
	return edges, err
}

func loadQuerySimilarities(db *store.DB, queryVec []float64) map[string]float64 {
	scores := make(map[string]float64)
	// The callback only computes a scalar; it never re-enters the single-conn DB.
	err := db.ScanEmbeddings(func(id string, blob []byte) bool {
		if vec := embed.DeserializeVector(blob); vec != nil {
			scores[id] = embed.CosineSimilarity(queryVec, vec)
		}
		return true
	})
	if err != nil {
		return nil // preserve recall's keyword-only fallback on embedding failure
	}
	return scores
}

package search

import (
	"container/heap"
	"github.com/mnemon-dev/mnemon/internal/memory/embed"
	"github.com/mnemon-dev/mnemon/internal/memory/model"
	"github.com/mnemon-dev/mnemon/internal/memory/store"
)

// referenceBeamSearch is the traversal from ceebe145. Keep its independent
// database reads and cosine calculations as an oracle for cached traversal.
func referenceBeamSearch(
	db *store.DB,
	startID string,
	startScore float64,
	queryVec []float64,
	weights IntentWeights,
	params TraversalParams,
	scoreMap map[string]float64,
	viaMap map[string]string,
	insightMap map[string]*model.Insight,
	embedCache map[string][]float64,
) {
	visited := map[string]bool{startID: true}
	totalVisited := 1

	// Seed the beam with the anchor
	current := &beamHeap{{id: startID, score: startScore, depth: 0}}
	heap.Init(current)

	for depth := 0; depth < params.MaxDepth; depth++ {
		if current.Len() == 0 || totalVisited >= params.MaxVisited {
			break
		}

		// Collect all candidates for the next level
		next := &beamHeap{}
		heap.Init(next)

		// Process all nodes at the current level
		for current.Len() > 0 && totalVisited < params.MaxVisited {
			cur := heap.Pop(current).(beamItem)
			if cur.depth != depth {
				// Put it back — it's for a future level
				heap.Push(current, cur)
				break
			}

			edges, err := db.GetEdgesByNode(cur.id)
			if err != nil {
				continue
			}

			for _, e := range edges {
				if totalVisited >= params.MaxVisited {
					break
				}
				neighborID := e.TargetID
				if neighborID == cur.id {
					neighborID = e.SourceID
				}

				// MAGMA transition score (P6): additive accumulation
				// score_v = score_u + λ₁·φ(edgeType, intent) + λ₂·sim(v_neighbor, v_query)
				structural := weights[e.EdgeType] * e.Weight // φ(edgeType, intent) * edge_weight
				semantic := 0.0
				if queryVec != nil && embedCache != nil {
					if nVec, ok := embedCache[neighborID]; ok {
						cosSim := embed.CosineSimilarity(queryVec, nVec)
						if cosSim > 0 {
							semantic = cosSim
						}
					}
				}
				neighborScore := cur.score + lambda1*structural + lambda2*semantic

				// Update global score map if this path is better
				if existing, ok := scoreMap[neighborID]; !ok || neighborScore > existing {
					scoreMap[neighborID] = neighborScore
					viaMap[neighborID] = string(e.EdgeType)
					if _, loaded := insightMap[neighborID]; !loaded {
						ins, err := db.GetInsightByID(neighborID)
						if err == nil && ins != nil {
							insightMap[neighborID] = ins
						}
					}
				}

				if !visited[neighborID] {
					visited[neighborID] = true
					totalVisited++
					heap.Push(next, beamItem{
						id:    neighborID,
						score: neighborScore,
						depth: depth + 1,
					})
				}
			}
		}

		// Prune beam: keep only top beamWidth candidates for next level
		pruned := &beamHeap{}
		heap.Init(pruned)
		for next.Len() > 0 && pruned.Len() < params.BeamWidth {
			heap.Push(pruned, heap.Pop(next).(beamItem))
		}
		current = pruned
	}
}

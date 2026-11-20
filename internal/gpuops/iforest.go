package gpuops

import (
	"math"
	"math/rand"
)

// IsolationForest is the unsupervised detector from Liu, Ting and Zhou (2008).
// Points that are isolated with few random splits score close to 1.
type IsolationForest struct {
	trees      []*itreeNode
	sampleSize int
}

type itreeNode struct {
	feature     int
	split       float64
	left, right *itreeNode
	size        int
}

const eulerGamma = 0.5772156649015329

// avgPathLength is c(n), the mean path length of an unsuccessful BST search.
func avgPathLength(n int) float64 {
	switch {
	case n <= 1:
		return 0
	case n == 2:
		return 1
	}
	f := float64(n)
	return 2*(math.Log(f-1)+eulerGamma) - 2*(f-1)/f
}

// FitForest trains trees on random subsamples of data. All rows must have the
// same width. The seed keeps scores reproducible for the same input.
func FitForest(data [][]float64, trees, sampleSize int, seed int64) *IsolationForest {
	if len(data) == 0 {
		return &IsolationForest{}
	}
	if trees <= 0 {
		trees = 100
	}
	if sampleSize <= 0 || sampleSize > len(data) {
		sampleSize = len(data)
	}
	rng := rand.New(rand.NewSource(seed))
	limit := int(math.Ceil(math.Log2(float64(sampleSize))))
	f := &IsolationForest{sampleSize: sampleSize, trees: make([]*itreeNode, 0, trees)}
	idx := make([]int, len(data))
	for i := range idx {
		idx[i] = i
	}
	for t := 0; t < trees; t++ {
		rng.Shuffle(len(idx), func(i, j int) { idx[i], idx[j] = idx[j], idx[i] })
		sample := make([][]float64, sampleSize)
		for i := 0; i < sampleSize; i++ {
			sample[i] = data[idx[i]]
		}
		f.trees = append(f.trees, buildITree(sample, 0, limit, rng))
	}
	return f
}

func buildITree(rows [][]float64, depth, limit int, rng *rand.Rand) *itreeNode {
	if depth >= limit || len(rows) <= 1 {
		return &itreeNode{size: len(rows)}
	}
	width := len(rows[0])
	var candidates []int
	mins := make([]float64, width)
	maxs := make([]float64, width)
	for j := 0; j < width; j++ {
		mins[j], maxs[j] = rows[0][j], rows[0][j]
		for _, r := range rows[1:] {
			mins[j] = math.Min(mins[j], r[j])
			maxs[j] = math.Max(maxs[j], r[j])
		}
		if maxs[j] > mins[j] {
			candidates = append(candidates, j)
		}
	}
	if len(candidates) == 0 {
		return &itreeNode{size: len(rows)}
	}
	feat := candidates[rng.Intn(len(candidates))]
	split := mins[feat] + rng.Float64()*(maxs[feat]-mins[feat])
	var left, right [][]float64
	for _, r := range rows {
		if r[feat] < split {
			left = append(left, r)
		} else {
			right = append(right, r)
		}
	}
	return &itreeNode{
		feature: feat,
		split:   split,
		left:    buildITree(left, depth+1, limit, rng),
		right:   buildITree(right, depth+1, limit, rng),
	}
}

func pathLength(n *itreeNode, x []float64, depth int) float64 {
	if n.left == nil {
		return float64(depth) + avgPathLength(n.size)
	}
	if x[n.feature] < n.split {
		return pathLength(n.left, x, depth+1)
	}
	return pathLength(n.right, x, depth+1)
}

// Score returns s(x) in (0, 1]. Around 0.5 is ordinary; near 1 is isolated.
// Points outside the training range score like the most extreme training
// point, so callers include the scored point in the training set.
func (f *IsolationForest) Score(x []float64) float64 {
	if f == nil || len(f.trees) == 0 {
		return 0
	}
	var total float64
	for _, t := range f.trees {
		total += pathLength(t, x, 0)
	}
	mean := total / float64(len(f.trees))
	c := avgPathLength(f.sampleSize)
	if c == 0 {
		return 0
	}
	return math.Pow(2, -mean/c)
}

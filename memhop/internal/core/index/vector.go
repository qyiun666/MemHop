// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"

	"github.com/qyiun666/memhop/memhop/internal/hash"
	"github.com/qyiun666/memhop/memhop/internal/core"
	"github.com/qyiun666/memhop/memhop/internal/core/storage"
)

// System record IDs for IVF persistence.
const (
	ivfClusterID uint64 = 1
	ivfBucketID  uint64 = 2
)

// IVF serialization magic bytes and version.
var (
	ivfClusterMagic = [4]byte{'M', 'H', 'I', 'V'}
	ivfBucketMagic  = [4]byte{'M', 'H', 'I', 'B'}
	ivfVersion      byte = 1
)

// BucketEntry stores a vector reference in an IVF bucket.
type BucketEntry struct {
	IDHash    uint64
	PageID    uint32
	SlotIndex uint16
}

// IVFIndex is an in-memory inverted file index for approximate nearest neighbor search.
type IVFIndex struct {
	Centroids    [][]uint16 // f16 centroid vectors
	Buckets      [][]BucketEntry
	Dim          int
	InitialK     int
	K            int
	centroidSums [][]float32 // running sum per centroid for incremental mean
	counts       []int
}

// NewIVFIndex creates an empty IVF index.
func NewIVFIndex(dim, initialK int) *IVFIndex {
	cap := initialK
	if cap < 1 {
		cap = 1
	}
	return &IVFIndex{
		Centroids:    make([][]uint16, 0, cap),
		Buckets:      make([][]BucketEntry, 0, cap),
		Dim:          dim,
		InitialK:     initialK,
		centroidSums: make([][]float32, 0, cap),
		counts:       make([]int, 0, cap),
	}
}

// Len returns the total number of vectors across all buckets.
func (ivf *IVFIndex) Len() int {
	total := 0
	for _, b := range ivf.Buckets {
		total += len(b)
	}
	return total
}

// AddVector assigns a vector to the nearest centroid bucket.
// Creates new centroids until InitialK is reached; afterwards uses incremental mean update.
func (ivf *IVFIndex) AddVector(idHash uint64, vector []uint16, pageID uint32, slotIndex uint16) {
	if len(vector) != ivf.Dim {
		panic(fmt.Sprintf("vector dimension mismatch: got %d, want %d", len(vector), ivf.Dim))
	}

	var idx int
	if len(ivf.Centroids) < ivf.InitialK {
		// Create new centroid.
		idx = len(ivf.Centroids)
		ivf.Centroids = append(ivf.Centroids, copyF16(vector))
		sums := make([]float32, ivf.Dim)
		for i, v := range vector {
			sums[i] = F16ToF32(v)
		}
		ivf.centroidSums = append(ivf.centroidSums, sums)
		ivf.counts = append(ivf.counts, 1)
		ivf.Buckets = append(ivf.Buckets, nil)
		ivf.K = len(ivf.Centroids)
	} else {
		// Find nearest centroid.
		idx = ivf.nearestCentroid(vector)
		// Update centroid with incremental mean.
		sum := ivf.centroidSums[idx]
		for i, v := range vector {
			sum[i] += F16ToF32(v)
		}
		ivf.counts[idx]++
		count := float32(ivf.counts[idx])
		for i, s := range sum {
			ivf.Centroids[idx][i] = F32ToF16(s / count)
		}
	}

	ivf.Buckets[idx] = append(ivf.Buckets[idx], BucketEntry{
		IDHash:    idHash,
		PageID:    pageID,
		SlotIndex: slotIndex,
	})
}

// nearestCentroid finds the index of the centroid most similar to vector.
func (ivf *IVFIndex) nearestCentroid(vector []uint16) int {
	bestIdx := 0
	bestScore := float32(-1e30)
	for i, c := range ivf.Centroids {
		s := CosineSimilarity(vector, c)
		if s > bestScore {
			bestScore = s
			bestIdx = i
		}
	}
	return bestIdx
}

// RebuildIfNeeded adjusts K to max(initialK, ceil(sqrt(totalVectors))).
func (ivf *IVFIndex) RebuildIfNeeded(totalVectors int) {
	if totalVectors == 0 {
		return
	}
	target := int(math.Ceil(math.Sqrt(float64(totalVectors))))
	if target < ivf.InitialK {
		target = ivf.InitialK
	}
	if target < 1 {
		target = 1
	}
	if target == ivf.K {
		return
	}

	oldBuckets := ivf.Buckets
	ivf.Buckets = make([][]BucketEntry, target)
	for i, b := range oldBuckets {
		dst := i
		if dst >= target {
			dst = 0
		}
		ivf.Buckets[dst] = append(ivf.Buckets[dst], b...)
	}

	// Adjust centroid arrays.
	if target < len(ivf.Centroids) {
		ivf.Centroids = ivf.Centroids[:target]
		ivf.centroidSums = ivf.centroidSums[:target]
		ivf.counts = ivf.counts[:target]
	} else {
		for len(ivf.Centroids) < target {
			ivf.Centroids = append(ivf.Centroids, make([]uint16, ivf.Dim))
			ivf.centroidSums = append(ivf.centroidSums, make([]float32, ivf.Dim))
			ivf.counts = append(ivf.counts, 0)
		}
	}
	ivf.K = target
}

// VecRecordHash computes the storage record hash for a topic's centroid vector.
func VecRecordHash(topicIDHash uint64) uint64 {
	return hash.HashID(fmt.Sprintf("v:%d", topicIDHash))
}

// IVFKNN performs approximate nearest neighbor search using IVF.
func IVFKNN(ivf *IVFIndex, engine *storage.StorageEngine, queryVector []uint16, kResults, nProbes int) ([]ScoredDoc, error) {
	if len(ivf.Centroids) == 0 || len(ivf.Buckets) == 0 || len(queryVector) != ivf.Dim {
		return nil, nil
	}

	probes := nProbes
	if probes > ivf.K {
		probes = ivf.K
	}
	if probes == 0 {
		return nil, nil
	}

	// Score centroids and select top probes.
	type centScore struct {
		idx   int
		score float32
	}
	scores := make([]centScore, len(ivf.Centroids))
	for i, c := range ivf.Centroids {
		scores[i] = centScore{idx: i, score: CosineSimilarity(queryVector, c)}
	}
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	seen := make(map[uint64]struct{})
	var candidates []ScoredDoc

	for p := 0; p < probes; p++ {
		bucketIdx := scores[p].idx
		for _, entry := range ivf.Buckets[bucketIdx] {
			if _, exists := seen[entry.IDHash]; exists {
				continue
			}
			seen[entry.IDHash] = struct{}{}
			vecHash := VecRecordHash(entry.IDHash)
			rt, data, err := engine.ReadRecord(vecHash)
			if err != nil {
				continue
			}
			_ = rt
			vec := decodeF16Vector(data, ivf.Dim)
			if len(vec) == ivf.Dim {
				sim := CosineSimilarity(queryVector, vec)
				candidates = append(candidates, ScoredDoc{IDHash: entry.IDHash, Score: sim})
			}
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})
	if kResults > 0 && len(candidates) > kResults {
		candidates = candidates[:kResults]
	}
	return candidates, nil
}

// decodeF16Vector reads f16 values from little-endian bytes.
func decodeF16Vector(data []byte, dim int) []uint16 {
	if len(data) < dim*2 {
		return nil
	}
	vec := make([]uint16, dim)
	for i := 0; i < dim; i++ {
		vec[i] = binary.LittleEndian.Uint16(data[i*2 : i*2+2])
	}
	return vec
}

// WriteIVFIndex persists centroids and buckets to the storage engine.
func WriteIVFIndex(engine *storage.StorageEngine, ivf *IVFIndex) error {
	// Delete old records.
	engine.DeleteRecord(ivfClusterID)
	engine.DeleteRecord(ivfBucketID)

	if len(ivf.Centroids) > 0 {
		cData := serializeCentroids(ivf)
		if _, err := engine.WriteRecord(storage.RecIVFCluster, ivfClusterID, cData); err != nil {
			return err
		}
	}
	if len(ivf.Buckets) > 0 {
		bData := serializeBuckets(ivf)
		if _, err := engine.WriteRecord(storage.RecIVFBucket, ivfBucketID, bData); err != nil {
			return err
		}
	}
	return nil
}

// ReadIVFIndex loads an IVF index from the storage engine. Returns nil if not found.
func ReadIVFIndex(engine *storage.StorageEngine) (*IVFIndex, error) {
	_, cData, err := engine.ReadRecord(ivfClusterID)
	if err != nil {
		return nil, nil // no IVF data
	}
	_, bData, err := engine.ReadRecord(ivfBucketID)
	if err != nil {
		return nil, nil
	}

	dim, k, centroids, err := deserializeCentroids(cData)
	if err != nil {
		return nil, err
	}
	buckets, err := deserializeBuckets(bData)
	if err != nil {
		return nil, err
	}

	// Rebuild centroid sums and counts.
	centroidSums := make([][]float32, k)
	counts := make([]int, k)
	for i, c := range centroids {
		count := len(buckets[i])
		if count < 1 {
			count = 1
		}
		counts[i] = count
		sums := make([]float32, dim)
		for j, v := range c {
			sums[j] = F16ToF32(v) * float32(count)
		}
		centroidSums[i] = sums
	}

	return &IVFIndex{
		Centroids:    centroids,
		Buckets:      buckets,
		Dim:          dim,
		InitialK:     k,
		K:            k,
		centroidSums: centroidSums,
		counts:       counts,
	}, nil
}

// --- serialization helpers ---

func serializeCentroids(ivf *IVFIndex) []byte {
	size := 12 + ivf.K*ivf.Dim*2
	buf := make([]byte, 0, size)
	buf = append(buf, ivfClusterMagic[:]...)
	buf = append(buf, ivfVersion, 0) // version + flags
	buf = appendLE16(buf, uint16(ivf.Dim))
	buf = appendLE32(buf, uint32(ivf.K))
	for _, c := range ivf.Centroids {
		for _, v := range c {
			buf = appendLE16(buf, v)
		}
	}
	return buf
}

func serializeBuckets(ivf *IVFIndex) []byte {
	buf := make([]byte, 0, 10+len(ivf.Buckets)*20)
	buf = append(buf, ivfBucketMagic[:]...)
	buf = append(buf, ivfVersion, 0)
	buf = appendLE32(buf, uint32(ivf.K))
	for _, bucket := range ivf.Buckets {
		buf = appendLE32(buf, uint32(len(bucket)))
		for _, e := range bucket {
			buf = appendLE64(buf, e.IDHash)
			buf = appendLE32(buf, e.PageID)
			buf = appendLE16(buf, e.SlotIndex)
		}
	}
	return buf
}

func deserializeCentroids(data []byte) (dim, k int, centroids [][]uint16, err error) {
	if len(data) < 12 {
		return 0, 0, nil, core.NewError(core.ErrDeserialization, "IVF centroid too short")
	}
	if data[0] != ivfClusterMagic[0] || data[1] != ivfClusterMagic[1] ||
		data[2] != ivfClusterMagic[2] || data[3] != ivfClusterMagic[3] {
		return 0, 0, nil, core.NewError(core.ErrDeserialization, "invalid IVF centroid magic")
	}
	if data[4] != ivfVersion {
		return 0, 0, nil, core.NewError(core.ErrDeserialization, "invalid IVF centroid version")
	}
	dim = int(binary.LittleEndian.Uint16(data[6:8]))
	k = int(binary.LittleEndian.Uint32(data[8:12]))
	if len(data) < 12+k*dim*2 {
		return 0, 0, nil, core.NewError(core.ErrDeserialization, "truncated IVF centroid data")
	}
	centroids = make([][]uint16, k)
	offset := 12
	for i := 0; i < k; i++ {
		c := make([]uint16, dim)
		for j := 0; j < dim; j++ {
			c[j] = binary.LittleEndian.Uint16(data[offset : offset+2])
			offset += 2
		}
		centroids[i] = c
	}
	return dim, k, centroids, nil
}

func deserializeBuckets(data []byte) ([][]BucketEntry, error) {
	if len(data) < 10 {
		return nil, core.NewError(core.ErrDeserialization, "IVF bucket too short")
	}
	if data[0] != ivfBucketMagic[0] || data[1] != ivfBucketMagic[1] ||
		data[2] != ivfBucketMagic[2] || data[3] != ivfBucketMagic[3] {
		return nil, core.NewError(core.ErrDeserialization, "invalid IVF bucket magic")
	}
	if data[4] != ivfVersion {
		return nil, core.NewError(core.ErrDeserialization, "invalid IVF bucket version")
	}
	k := int(binary.LittleEndian.Uint32(data[6:10]))
	buckets := make([][]BucketEntry, k)
	offset := 10
	for i := 0; i < k; i++ {
		if offset+4 > len(data) {
			return nil, core.NewError(core.ErrDeserialization, "truncated IVF bucket header")
		}
		count := int(binary.LittleEndian.Uint32(data[offset : offset+4]))
		offset += 4
		bucket := make([]BucketEntry, 0, count)
		for j := 0; j < count; j++ {
			if offset+14 > len(data) {
				return nil, core.NewError(core.ErrDeserialization, "truncated IVF bucket entry")
			}
			entry := BucketEntry{
				IDHash:    binary.LittleEndian.Uint64(data[offset : offset+8]),
				PageID:    binary.LittleEndian.Uint32(data[offset+8 : offset+12]),
				SlotIndex: binary.LittleEndian.Uint16(data[offset+12 : offset+14]),
			}
			bucket = append(bucket, entry)
			offset += 14
		}
		buckets[i] = bucket
	}
	return buckets, nil
}

// --- byte helpers ---

func appendLE16(buf []byte, v uint16) []byte {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	return append(buf, b[:]...)
}

func appendLE32(buf []byte, v uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return append(buf, b[:]...)
}

func appendLE64(buf []byte, v uint64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	return append(buf, b[:]...)
}

func copyF16(v []uint16) []uint16 {
	c := make([]uint16, len(v))
	copy(c, v)
	return c
}

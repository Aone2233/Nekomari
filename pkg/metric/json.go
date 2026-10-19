package metric

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
)

// encodeMap encodes a string map as JSON.
//
// encodeMap 将字符串 map 编码为 JSON 字符串，nil 会按空对象处理。
func encodeMap(m map[string]string) (string, error) {
	if m == nil {
		m = map[string]string{}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// decodeMap decodes a scanned JSON value into a string map.
//
// decodeMap 将数据库扫描出的 JSON 值还原为字符串 map。
func decodeMap(v any) (map[string]string, error) {
	switch x := v.(type) {
	case nil:
		return map[string]string{}, nil
	case string:
		return decodeMapString(x)
	case []byte:
		return decodeMapString(string(x))
	default:
		return nil, fmt.Errorf("unsupported json value type %T", v)
	}
}

// decodeMapString decodes a JSON object string into a string map.
//
// decodeMapString 将 JSON 字符串解码为字符串 map。
func decodeMapString(s string) (map[string]string, error) {
	if s == "" {
		return map[string]string{}, nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]string{}
	}
	return out, nil
}

// canonicalTags returns a deterministic JSON encoding of a tag map. encodeMap
// already relies on encoding/json, which sorts map keys, so equal tag maps
// always produce identical bytes — the property tagsFingerprint depends on.
//
// canonicalTags 返回标签 map 的确定性 JSON 编码；相同标签集合总会得到
// 相同字节序列，tagsFingerprint 依赖这个性质。
func canonicalTags(m map[string]string) (string, error) {
	return encodeMap(m)
}

const (
	// fingerprintCacheLimit bounds the canonical fingerprint cache. Tag and
	// label sets repeat heavily across the points of a report batch, so a small
	// content-addressed cache removes nearly every repeated SHA-256 and hex
	// conversion. Reaching the limit clears the whole map: entries are a few
	// dozen bytes and rebuild cheaply, so incremental eviction buys nothing.
	//
	// fingerprintCacheLimit 限制 canonical 指纹缓存的容量。一个上报批内标签和
	// 标签说明重复度很高，因此一个小的按内容寻址的缓存几乎能消除所有重复的
	// SHA-256 与十六进制转换。达到上限时整体清空缓存：单条记录只有几十字节且重建
	// 成本很低，增量淘汰没有收益。
	fingerprintCacheLimit = 65536
)

// emptyTagsCanonical is the canonical JSON encoding of an empty (or nil) tag
// set; emptyTagsFingerprint is its fingerprint, shared by both forms.
//
// emptyTagsCanonical 是空（或 nil）标签集合的 canonical JSON 编码；
// emptyTagsFingerprint 是其指纹，两种形式共用。
var (
	emptyTagsCanonical   = "{}"
	emptyTagsFingerprint = hashCanonicalTags(emptyTagsCanonical)
)

// fingerprintComputations counts how many canonical tag strings were actually
// hashed. Tests assert that a repeated canonical input is served from the cache
// and therefore leaves this counter untouched.
//
// fingerprintComputations 记录实际计算过哈希的 canonical 标签字符串数量。测试
// 用它断言重复的 canonical 输入由缓存命中，因而不会增加这个计数。
var fingerprintComputations atomic.Int64

// fingerprintCache memoizes hex fingerprints keyed by canonical JSON.
//
// fingerprintCache 以 canonical JSON 为键缓存十六进制指纹。
var fingerprintCache = struct {
	mu      sync.RWMutex
	entries map[string]string
}{entries: make(map[string]string, 256)}

// hashCanonicalTags computes the hex SHA-256 of one canonical tag string.
//
// hashCanonicalTags 计算单个 canonical 标签字符串的十六进制 SHA-256。
func hashCanonicalTags(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// tagsFingerprint returns a stable hex fingerprint of a tag set, used as the
// rollups table's tags_hash key column. Equal tag maps (regardless of Go map
// iteration order) hash identically, so each distinct tag combination becomes
// its own rollup series; an empty/nil tag map hashes to the fingerprint of "{}".
//
// Repeated canonical tag strings are served from a bounded cache, which keeps
// the ingest path from re-hashing the same tag and label sets for every point.
// A nil map and an empty map share one canonical form and therefore one
// fingerprint.
//
// tagsFingerprint 为标签集合生成稳定的十六进制指纹，用作 rollups 表的
// tags_hash key 列。相同标签 map（无论 Go map 迭代顺序如何）都会得到相同 hash，
// 因此每种不同标签组合都会成为自己的 rollup 序列；空或 nil 标签 map 会得到 "{}"
// 的指纹。
//
// 重复出现的 canonical 标签字符串由有界缓存直接返回，写入路径不必为每个点重复
// 计算同一组标签与标签说明。nil map 与空 map 共用同一种 canonical 形式，因此
// 指纹也相同。
func tagsFingerprint(m map[string]string) (hash string, canonical string, err error) {
	if len(m) == 0 {
		return emptyTagsFingerprint, emptyTagsCanonical, nil
	}
	canonical, err = canonicalTags(m)
	if err != nil {
		return "", "", err
	}

	fingerprintCache.mu.RLock()
	cached, ok := fingerprintCache.entries[canonical]
	fingerprintCache.mu.RUnlock()
	if ok {
		return cached, canonical, nil
	}

	hash = hashCanonicalTags(canonical)
	fingerprintComputations.Add(1)

	fingerprintCache.mu.Lock()
	if len(fingerprintCache.entries) >= fingerprintCacheLimit {
		fingerprintCache.entries = make(map[string]string, fingerprintCacheLimit)
	}
	fingerprintCache.entries[canonical] = hash
	fingerprintCache.mu.Unlock()
	return hash, canonical, nil
}

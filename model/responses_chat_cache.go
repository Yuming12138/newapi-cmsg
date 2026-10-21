package model

import (
	"container/list"
	"sync"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
)

const (
	responsesChatCacheDefaultBytes     = 48 << 20
	responsesChatCacheDefaultRetention = int64(7 * 24 * 60 * 60)
)

type responsesChatCacheEntry struct {
	id    string
	value ResponsesChatSession
	size  int64
}

type responsesChatSessionCache struct {
	mu       sync.Mutex
	maxBytes int64
	used     int64
	order    *list.List
	items    map[string]*list.Element
}

var (
	responsesChatCacheOnce      sync.Once
	responsesChatCacheInstance  *responsesChatSessionCache
	responsesChatCacheHits      atomic.Uint64
	responsesChatCacheMisses    atomic.Uint64
	responsesChatCacheEvictions atomic.Uint64
)

func newResponsesChatSessionCache(maxBytes int64) *responsesChatSessionCache {
	if maxBytes < 0 {
		maxBytes = 0
	}
	return &responsesChatSessionCache{
		maxBytes: maxBytes,
		order:    list.New(),
		items:    make(map[string]*list.Element),
	}
}

func getResponsesChatSessionCache() *responsesChatSessionCache {
	responsesChatCacheOnce.Do(func() {
		maxBytes := int64(common.GetEnvOrDefault("RESPONSES_SESSION_CACHE_BYTES", responsesChatCacheDefaultBytes))
		responsesChatCacheInstance = newResponsesChatSessionCache(maxBytes)
	})
	return responsesChatCacheInstance
}

func responsesChatSessionSize(session *ResponsesChatSession) int64 {
	if session == nil {
		return 0
	}
	return int64(len(session.ID) + len(session.ModelName) + len(session.Items) + len(session.PendingToolCalls) + 128)
}

func responsesChatSessionCacheExpired(session *ResponsesChatSession) bool {
	if session == nil {
		return true
	}
	retentionSeconds := int64(common.GetEnvOrDefault("RESPONSES_SESSION_RETENTION_SECONDS", int(responsesChatCacheDefaultRetention)))
	if retentionSeconds <= 0 {
		return false
	}
	updatedAt := session.UpdatedAt
	if updatedAt == 0 {
		updatedAt = session.CreatedAt
	}
	return updatedAt > 0 && updatedAt < common.GetTimestamp()-retentionSeconds
}

func (cache *responsesChatSessionCache) get(id string) (*ResponsesChatSession, bool) {
	if cache == nil || cache.maxBytes == 0 || id == "" {
		return nil, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	element, ok := cache.items[id]
	if !ok {
		return nil, false
	}
	cache.order.MoveToFront(element)
	entry := element.Value.(*responsesChatCacheEntry)
	if responsesChatSessionCacheExpired(&entry.value) {
		delete(cache.items, entry.id)
		cache.order.Remove(element)
		cache.used -= entry.size
		responsesChatCacheEvictions.Add(1)
		return nil, false
	}
	value := entry.value
	return &value, true
}

func (cache *responsesChatSessionCache) put(session *ResponsesChatSession) {
	if cache == nil || cache.maxBytes == 0 || session == nil || session.ID == "" {
		return
	}
	size := responsesChatSessionSize(session)
	if size <= 0 || size > cache.maxBytes {
		return
	}
	value := *session
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if existing, ok := cache.items[value.ID]; ok {
		entry := existing.Value.(*responsesChatCacheEntry)
		cache.used -= entry.size
		entry.value = value
		entry.size = size
		cache.used += size
		cache.order.MoveToFront(existing)
		cache.trimLocked()
		return
	}
	entry := &responsesChatCacheEntry{id: value.ID, value: value, size: size}
	cache.items[value.ID] = cache.order.PushFront(entry)
	cache.used += size
	cache.trimLocked()
}

func (cache *responsesChatSessionCache) trimLocked() {
	for cache.used > cache.maxBytes && cache.order.Len() > 0 {
		element := cache.order.Back()
		if element == nil {
			return
		}
		entry := element.Value.(*responsesChatCacheEntry)
		delete(cache.items, entry.id)
		cache.order.Remove(element)
		cache.used -= entry.size
		responsesChatCacheEvictions.Add(1)
	}
}

func (cache *responsesChatSessionCache) clear() {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.order.Init()
	cache.items = make(map[string]*list.Element)
	cache.used = 0
}

func cacheResponsesChatSession(session *ResponsesChatSession) {
	if session == nil {
		return
	}
	getResponsesChatSessionCache().put(session)
}

func cachedResponsesChatSession(id string) (*ResponsesChatSession, bool) {
	session, ok := getResponsesChatSessionCache().get(id)
	if ok {
		responsesChatCacheHits.Add(1)
	} else {
		responsesChatCacheMisses.Add(1)
	}
	return session, ok
}

func clearResponsesChatSessionCacheForTest() {
	getResponsesChatSessionCache().clear()
}

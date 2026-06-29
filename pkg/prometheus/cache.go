package prometheus

import (
	"container/list"
	"fmt"
	"sync"
)

// clientCacheEntry 缓存项：以 datasource_id 为 key，但 URL/账号变更时需要失效
type clientCacheEntry struct {
	id       uint
	url      string
	username string
	password string
	client   *Client
	element  *list.Element
}

// ClientCache 是一个固定容量的 LRU 缓存，键为 datasource.ID。
//
// 设计要点：
//   - 单进程内复用 *Client（即复用底层 *http.Transport），避免几千数据源场景下
//     每次评估都新建 TCP / TLS 连接。
//   - 同一 datasource 的 URL / 账号变更时自动失效。
//   - 容量超限时按 LRU 顺序淘汰最久未使用的 Client（让它的 transport 被 GC）。
type ClientCache struct {
	mu       sync.Mutex
	capacity int
	items    map[uint]*clientCacheEntry
	order    *list.List // front = most recently used
}

// NewClientCache 创建指定容量的 LRU 缓存
func NewClientCache(capacity int) *ClientCache {
	if capacity <= 0 {
		capacity = 1024
	}
	return &ClientCache{
		capacity: capacity,
		items:    make(map[uint]*clientCacheEntry, capacity),
		order:    list.New(),
	}
}

// Get 返回与 (id, url, user, pass) 对应的 Client。若不在缓存或元数据变更则重建。
// id=0 表示不缓存（直接新建并返回），用于一次性测试连接等场景。
func (c *ClientCache) Get(id uint, url, username, password string) (*Client, error) {
	if id == 0 {
		return NewClient(url, username, password)
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, ok := c.items[id]; ok {
		if entry.url == url && entry.username == username && entry.password == password {
			c.order.MoveToFront(entry.element)
			return entry.client, nil
		}
		// 元数据变了，剔除旧条目
		c.order.Remove(entry.element)
		delete(c.items, id)
	}

	client, err := NewClient(url, username, password)
	if err != nil {
		return nil, fmt.Errorf("creating prometheus client for ds=%d: %w", id, err)
	}

	entry := &clientCacheEntry{
		id:       id,
		url:      url,
		username: username,
		password: password,
		client:   client,
	}
	entry.element = c.order.PushFront(entry)
	c.items[id] = entry

	// LRU 淘汰
	for c.order.Len() > c.capacity {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		old := oldest.Value.(*clientCacheEntry)
		c.order.Remove(oldest)
		delete(c.items, old.id)
	}
	return client, nil
}

// Invalidate 移除指定 datasource 的缓存条目（数据源被禁用/删除时调用）
func (c *ClientCache) Invalidate(id uint) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.items[id]; ok {
		c.order.Remove(entry.element)
		delete(c.items, id)
	}
}

// Len 返回当前缓存条目数（测试/监控用）
func (c *ClientCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

// DefaultCache 进程内默认共享缓存（评估器、admin handler 都可使用）
var DefaultCache = NewClientCache(2048)

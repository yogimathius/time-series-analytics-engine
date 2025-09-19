package enterprise

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"sync"
	"time"
)

// ClusterManager handles distributed time-series operations across multiple nodes
type ClusterManager struct {
	nodeID       string
	nodes        map[string]*ClusterNode
	replication  *ReplicationConfig
	partitioner  *ConsistentHashPartitioner
	mutex        sync.RWMutex
	httpClient   *http.Client
	isLeader     bool
	leaderID     string
}

// ClusterNode represents a node in the cluster
type ClusterNode struct {
	ID              string    `json:"id"`
	Address         string    `json:"address"`
	Port            int       `json:"port"`
	Status          string    `json:"status"` // active, inactive, draining
	LastHeartbeat   time.Time `json:"last_heartbeat"`
	DataPartitions  []int     `json:"data_partitions"`
	ResourceUsage   *NodeResourceUsage `json:"resource_usage"`
}

// NodeResourceUsage tracks node-level resource consumption
type NodeResourceUsage struct {
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryPercent float64 `json:"memory_percent"`
	DiskPercent   float64 `json:"disk_percent"`
	NetworkMbps   float64 `json:"network_mbps"`
	QueriesPerSec int     `json:"queries_per_sec"`
	PointsPerSec  int     `json:"points_per_sec"`
}

// ReplicationConfig defines data replication behavior
type ReplicationConfig struct {
	Factor              int           `json:"factor"`              // Number of replicas
	ConsistencyLevel    string        `json:"consistency_level"`   // eventual, strong
	ReadQuorum          int           `json:"read_quorum"`
	WriteQuorum         int           `json:"write_quorum"`
	SyncTimeout         time.Duration `json:"sync_timeout"`
	RepairInterval      time.Duration `json:"repair_interval"`
	EnableAntiEntropy   bool          `json:"enable_anti_entropy"`
}

// ConsistentHashPartitioner implements consistent hashing for data distribution
type ConsistentHashPartitioner struct {
	partitions      int
	replicaFactor   int
	nodeHashes      map[uint32]string
	sortedHashes    []uint32
	virtualNodes    int
	mutex           sync.RWMutex
}

// DataPartition represents a logical partition of time-series data
type DataPartition struct {
	ID          int       `json:"id"`
	StartToken  uint32    `json:"start_token"`
	EndToken    uint32    `json:"end_token"`
	PrimaryNode string    `json:"primary_node"`
	Replicas    []string  `json:"replicas"`
	Status      string    `json:"status"` // healthy, degraded, unavailable
	LastSync    time.Time `json:"last_sync"`
}

// NewClusterManager creates a new cluster manager
func NewClusterManager(nodeID, address string, port int, replicationConfig *ReplicationConfig) *ClusterManager {
	partitioner := NewConsistentHashPartitioner(1024, replicationConfig.Factor, 150)
	
	cm := &ClusterManager{
		nodeID:      nodeID,
		nodes:       make(map[string]*ClusterNode),
		replication: replicationConfig,
		partitioner: partitioner,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	
	// Add self to cluster
	selfNode := &ClusterNode{
		ID:            nodeID,
		Address:       address,
		Port:          port,
		Status:        "active",
		LastHeartbeat: time.Now(),
		ResourceUsage: &NodeResourceUsage{},
	}
	cm.nodes[nodeID] = selfNode
	cm.partitioner.AddNode(nodeID)
	
	// Start cluster maintenance routines
	go cm.startHeartbeat()
	go cm.startResourceMonitoring()
	go cm.startDataRepair()
	
	return cm
}

// AddNode adds a new node to the cluster
func (cm *ClusterManager) AddNode(nodeID, address string, port int) error {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()
	
	if _, exists := cm.nodes[nodeID]; exists {
		return fmt.Errorf("node %s already exists in cluster", nodeID)
	}
	
	node := &ClusterNode{
		ID:            nodeID,
		Address:       address,
		Port:          port,
		Status:        "active",
		LastHeartbeat: time.Now(),
		ResourceUsage: &NodeResourceUsage{},
	}
	
	cm.nodes[nodeID] = node
	cm.partitioner.AddNode(nodeID)
	
	log.Printf("Added node %s to cluster at %s:%d", nodeID, address, port)
	return nil
}

// RemoveNode removes a node from the cluster
func (cm *ClusterManager) RemoveNode(nodeID string) error {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()
	
	if _, exists := cm.nodes[nodeID]; !exists {
		return fmt.Errorf("node %s not found in cluster", nodeID)
	}
	
	// Mark node as draining before removal
	cm.nodes[nodeID].Status = "draining"
	
	// Trigger data migration (simplified)
	go cm.migrateDataFromNode(nodeID)
	
	return nil
}

// GetPartitionForKey determines which partition a time series belongs to
func (cm *ClusterManager) GetPartitionForKey(seriesKey string) ([]string, error) {
	return cm.partitioner.GetNodes(seriesKey), nil
}

// WriteDataPoint writes a data point to appropriate cluster nodes
func (cm *ClusterManager) WriteDataPoint(ctx context.Context, seriesKey string, dataPoint interface{}) error {
	nodes, err := cm.GetPartitionForKey(seriesKey)
	if err != nil {
		return err
	}
	
	// Write to required number of nodes based on write quorum
	writeQuorum := cm.replication.WriteQuorum
	if writeQuorum > len(nodes) {
		writeQuorum = len(nodes)
	}
	
	successCount := 0
	var lastError error
	
	for i, nodeID := range nodes {
		if i >= writeQuorum && successCount >= writeQuorum {
			break
		}
		
		if nodeID == cm.nodeID {
			// Local write
			successCount++
			continue
		}
		
		// Remote write
		if err := cm.writeToRemoteNode(ctx, nodeID, seriesKey, dataPoint); err != nil {
			lastError = err
			log.Printf("Failed to write to node %s: %v", nodeID, err)
		} else {
			successCount++
		}
	}
	
	if successCount < writeQuorum {
		return fmt.Errorf("failed to achieve write quorum (%d/%d): %v", successCount, writeQuorum, lastError)
	}
	
	return nil
}

// ReadDataPoints reads data points from cluster nodes
func (cm *ClusterManager) ReadDataPoints(ctx context.Context, seriesKey string, startTime, endTime time.Time) (interface{}, error) {
	nodes, err := cm.GetPartitionForKey(seriesKey)
	if err != nil {
		return nil, err
	}
	
	// Read from required number of nodes based on read quorum
	readQuorum := cm.replication.ReadQuorum
	if readQuorum > len(nodes) {
		readQuorum = len(nodes)
	}
	
	responses := make([]interface{}, 0, readQuorum)
	var lastError error
	
	for i, nodeID := range nodes {
		if len(responses) >= readQuorum {
			break
		}
		
		var data interface{}
		if nodeID == cm.nodeID {
			// Local read
			// data = cm.localStorage.Read(seriesKey, startTime, endTime)
			responses = append(responses, data)
		} else {
			// Remote read
			if data, err := cm.readFromRemoteNode(ctx, nodeID, seriesKey, startTime, endTime); err != nil {
				lastError = err
				log.Printf("Failed to read from node %s: %v", nodeID, err)
			} else {
				responses = append(responses, data)
			}
		}
	}
	
	if len(responses) < readQuorum {
		return nil, fmt.Errorf("failed to achieve read quorum (%d/%d): %v", len(responses), readQuorum, lastError)
	}
	
	// Merge responses (simplified - in practice would need sophisticated merging)
	return cm.mergeReadResponses(responses), nil
}

// writeToRemoteNode sends a write request to a remote node
func (cm *ClusterManager) writeToRemoteNode(ctx context.Context, nodeID, seriesKey string, dataPoint interface{}) error {
	node, exists := cm.nodes[nodeID]
	if !exists {
		return fmt.Errorf("node %s not found", nodeID)
	}
	
	url := fmt.Sprintf("http://%s:%d/internal/write", node.Address, node.Port)
	
	payload := map[string]interface{}{
		"series_key":  seriesKey,
		"data_point": dataPoint,
	}
	
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Cluster-Node", cm.nodeID)
	
	resp, err := cm.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("remote write failed with status %d", resp.StatusCode)
	}
	
	return nil
}

// readFromRemoteNode sends a read request to a remote node
func (cm *ClusterManager) readFromRemoteNode(ctx context.Context, nodeID, seriesKey string, startTime, endTime time.Time) (interface{}, error) {
	node, exists := cm.nodes[nodeID]
	if !exists {
		return nil, fmt.Errorf("node %s not found", nodeID)
	}
	
	url := fmt.Sprintf("http://%s:%d/internal/read?series=%s&start=%d&end=%d", 
		node.Address, node.Port, seriesKey, startTime.Unix(), endTime.Unix())
	
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Cluster-Node", cm.nodeID)
	
	resp, err := cm.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("remote read failed with status %d", resp.StatusCode)
	}
	
	var result interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	
	return result, nil
}

// mergeReadResponses combines responses from multiple nodes
func (cm *ClusterManager) mergeReadResponses(responses []interface{}) interface{} {
	// Simplified merging - in practice would need conflict resolution
	if len(responses) > 0 {
		return responses[0]
	}
	return nil
}

// startHeartbeat maintains cluster membership through heartbeats
func (cm *ClusterManager) startHeartbeat() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for range ticker.C {
		cm.sendHeartbeats()
		cm.checkNodeHealth()
	}
}

// sendHeartbeats sends heartbeat to all cluster nodes
func (cm *ClusterManager) sendHeartbeats() {
	cm.mutex.RLock()
	nodes := make([]*ClusterNode, 0, len(cm.nodes))
	for _, node := range cm.nodes {
		if node.ID != cm.nodeID {
			nodes = append(nodes, node)
		}
	}
	cm.mutex.RUnlock()
	
	for _, node := range nodes {
		go cm.sendHeartbeatToNode(node)
	}
}

// sendHeartbeatToNode sends heartbeat to a specific node
func (cm *ClusterManager) sendHeartbeatToNode(node *ClusterNode) {
	url := fmt.Sprintf("http://%s:%d/internal/heartbeat", node.Address, node.Port)
	
	payload := map[string]interface{}{
		"node_id":   cm.nodeID,
		"timestamp": time.Now().Unix(),
	}
	
	data, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return
	}
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	
	resp, err := cm.httpClient.Do(req)
	if err != nil {
		log.Printf("Heartbeat to node %s failed: %v", node.ID, err)
		return
	}
	defer resp.Body.Close()
	
	if resp.StatusCode == http.StatusOK {
		cm.mutex.Lock()
		if existingNode, exists := cm.nodes[node.ID]; exists {
			existingNode.LastHeartbeat = time.Now()
			existingNode.Status = "active"
		}
		cm.mutex.Unlock()
	}
}

// checkNodeHealth monitors node health and handles failures
func (cm *ClusterManager) checkNodeHealth() {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()
	
	for nodeID, node := range cm.nodes {
		if nodeID == cm.nodeID {
			continue
		}
		
		if time.Since(node.LastHeartbeat) > 90*time.Second {
			if node.Status == "active" {
				log.Printf("Node %s appears to be down, marking as inactive", nodeID)
				node.Status = "inactive"
				// Trigger data rebalancing
				go cm.handleNodeFailure(nodeID)
			}
		}
	}
}

// handleNodeFailure handles when a node fails
func (cm *ClusterManager) handleNodeFailure(nodeID string) {
	log.Printf("Handling failure of node %s", nodeID)
	// In a real implementation, this would:
	// 1. Redistribute the failed node's partitions
	// 2. Increase replication factor temporarily
	// 3. Alert administrators
	// 4. Update cluster topology
}

// migrateDataFromNode handles data migration during node removal
func (cm *ClusterManager) migrateDataFromNode(nodeID string) {
	log.Printf("Starting data migration from node %s", nodeID)
	// Implementation would handle actual data migration
}

// startResourceMonitoring monitors cluster resource usage
func (cm *ClusterManager) startResourceMonitoring() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	
	for range ticker.C {
		cm.collectResourceMetrics()
	}
}

// collectResourceMetrics gathers resource usage from all nodes
func (cm *ClusterManager) collectResourceMetrics() {
	// Implementation would collect CPU, memory, disk, network metrics
	// and update node resource usage
}

// startDataRepair handles data consistency repair
func (cm *ClusterManager) startDataRepair() {
	if !cm.replication.EnableAntiEntropy {
		return
	}
	
	ticker := time.NewTicker(cm.replication.RepairInterval)
	defer ticker.Stop()
	
	for range ticker.C {
		cm.performDataRepair()
	}
}

// performDataRepair checks and repairs data inconsistencies
func (cm *ClusterManager) performDataRepair() {
	log.Println("Starting data consistency repair")
	// Implementation would compare data across replicas and repair inconsistencies
}

// NewConsistentHashPartitioner creates a new consistent hash partitioner
func NewConsistentHashPartitioner(partitions, replicaFactor, virtualNodes int) *ConsistentHashPartitioner {
	return &ConsistentHashPartitioner{
		partitions:    partitions,
		replicaFactor: replicaFactor,
		nodeHashes:    make(map[uint32]string),
		virtualNodes:  virtualNodes,
	}
}

// AddNode adds a node to the hash ring
func (p *ConsistentHashPartitioner) AddNode(nodeID string) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	
	for i := 0; i < p.virtualNodes; i++ {
		hash := p.hash(fmt.Sprintf("%s:%d", nodeID, i))
		p.nodeHashes[hash] = nodeID
		p.sortedHashes = append(p.sortedHashes, hash)
	}
	
	// Keep hashes sorted
	for i := 0; i < len(p.sortedHashes); i++ {
		for j := i + 1; j < len(p.sortedHashes); j++ {
			if p.sortedHashes[i] > p.sortedHashes[j] {
				p.sortedHashes[i], p.sortedHashes[j] = p.sortedHashes[j], p.sortedHashes[i]
			}
		}
	}
}

// GetNodes returns the nodes responsible for a given key
func (p *ConsistentHashPartitioner) GetNodes(key string) []string {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	
	if len(p.sortedHashes) == 0 {
		return []string{}
	}
	
	hash := p.hash(key)
	nodes := make([]string, 0, p.replicaFactor)
	seen := make(map[string]bool)
	
	// Find the first node in the ring
	idx := 0
	for i, h := range p.sortedHashes {
		if h >= hash {
			idx = i
			break
		}
	}
	
	// Collect nodes with wraparound
	for len(nodes) < p.replicaFactor && len(seen) < len(p.nodeHashes) {
		nodeID := p.nodeHashes[p.sortedHashes[idx]]
		if !seen[nodeID] {
			nodes = append(nodes, nodeID)
			seen[nodeID] = true
		}
		idx = (idx + 1) % len(p.sortedHashes)
	}
	
	return nodes
}

// hash generates a hash value for a key
func (p *ConsistentHashPartitioner) hash(key string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(key))
	return h.Sum32()
}
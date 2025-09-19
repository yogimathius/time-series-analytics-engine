package enterprise

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// TenantManager handles multi-tenant isolation and resource management
type TenantManager struct {
	tenants map[string]*Tenant
	mutex   sync.RWMutex
	config  *MultiTenancyConfig
}

// Tenant represents a single tenant with isolated resources
type Tenant struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Plan            string    `json:"plan"`
	CreatedAt       time.Time `json:"created_at"`
	ResourceLimits  *ResourceLimits `json:"resource_limits"`
	CurrentUsage    *ResourceUsage  `json:"current_usage"`
	Status          string    `json:"status"` // active, suspended, deleted
	StorageEngine   interface{} `json:"-"` // Isolated storage per tenant
	mutex           sync.RWMutex
}

// ResourceLimits defines tenant-specific resource constraints
type ResourceLimits struct {
	MaxSeries         int   `json:"max_series"`
	MaxPointsPerDay   int64 `json:"max_points_per_day"`
	MaxQueryRate      int   `json:"max_query_rate"`      // queries per minute
	MaxRetentionDays  int   `json:"max_retention_days"`
	MaxStorageGB      int   `json:"max_storage_gb"`
	MaxConcurrentQueries int `json:"max_concurrent_queries"`
}

// ResourceUsage tracks current tenant resource consumption
type ResourceUsage struct {
	SeriesCount       int   `json:"series_count"`
	PointsToday       int64 `json:"points_today"`
	QueryRate         int   `json:"current_query_rate"`
	StorageUsedGB     float64 `json:"storage_used_gb"`
	ActiveQueries     int   `json:"active_queries"`
	LastUpdated       time.Time `json:"last_updated"`
}

// MultiTenancyConfig configures multi-tenant behavior
type MultiTenancyConfig struct {
	Enabled                bool          `json:"enabled"`
	DefaultPlan           string        `json:"default_plan"`
	ResourceCheckInterval time.Duration `json:"resource_check_interval"`
	Plans                 map[string]*ResourceLimits `json:"plans"`
}

// NewTenantManager creates a new tenant manager
func NewTenantManager(config *MultiTenancyConfig) *TenantManager {
	tm := &TenantManager{
		tenants: make(map[string]*Tenant),
		config:  config,
	}
	
	// Start resource monitoring
	go tm.startResourceMonitoring()
	
	return tm
}

// CreateTenant creates a new tenant with specified plan
func (tm *TenantManager) CreateTenant(tenantID, name, plan string) (*Tenant, error) {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()
	
	if _, exists := tm.tenants[tenantID]; exists {
		return nil, fmt.Errorf("tenant %s already exists", tenantID)
	}
	
	limits, exists := tm.config.Plans[plan]
	if !exists {
		return nil, fmt.Errorf("plan %s not found", plan)
	}
	
	tenant := &Tenant{
		ID:         tenantID,
		Name:       name,
		Plan:       plan,
		CreatedAt:  time.Now(),
		ResourceLimits: limits,
		CurrentUsage: &ResourceUsage{
			LastUpdated: time.Now(),
		},
		Status: "active",
	}
	
	tm.tenants[tenantID] = tenant
	return tenant, nil
}

// GetTenant retrieves a tenant by ID
func (tm *TenantManager) GetTenant(tenantID string) (*Tenant, error) {
	tm.mutex.RLock()
	defer tm.mutex.RUnlock()
	
	tenant, exists := tm.tenants[tenantID]
	if !exists {
		return nil, fmt.Errorf("tenant %s not found", tenantID)
	}
	
	return tenant, nil
}

// ValidateResourceUsage checks if tenant is within resource limits
func (tm *TenantManager) ValidateResourceUsage(tenantID string, resourceType string, requestedAmount int64) error {
	tenant, err := tm.GetTenant(tenantID)
	if err != nil {
		return err
	}
	
	tenant.mutex.RLock()
	defer tenant.mutex.RUnlock()
	
	switch resourceType {
	case "series":
		if tenant.CurrentUsage.SeriesCount >= tenant.ResourceLimits.MaxSeries {
			return fmt.Errorf("tenant %s exceeded max series limit (%d)", tenantID, tenant.ResourceLimits.MaxSeries)
		}
	case "points":
		if tenant.CurrentUsage.PointsToday+requestedAmount > tenant.ResourceLimits.MaxPointsPerDay {
			return fmt.Errorf("tenant %s would exceed daily points limit (%d)", tenantID, tenant.ResourceLimits.MaxPointsPerDay)
		}
	case "queries":
		if tenant.CurrentUsage.ActiveQueries >= tenant.ResourceLimits.MaxConcurrentQueries {
			return fmt.Errorf("tenant %s exceeded concurrent query limit (%d)", tenantID, tenant.ResourceLimits.MaxConcurrentQueries)
		}
	case "query_rate":
		if tenant.CurrentUsage.QueryRate >= tenant.ResourceLimits.MaxQueryRate {
			return fmt.Errorf("tenant %s exceeded query rate limit (%d/min)", tenantID, tenant.ResourceLimits.MaxQueryRate)
		}
	}
	
	return nil
}

// UpdateResourceUsage updates tenant resource consumption
func (tm *TenantManager) UpdateResourceUsage(tenantID string, usage *ResourceUsage) error {
	tenant, err := tm.GetTenant(tenantID)
	if err != nil {
		return err
	}
	
	tenant.mutex.Lock()
	defer tenant.mutex.Unlock()
	
	tenant.CurrentUsage = usage
	tenant.CurrentUsage.LastUpdated = time.Now()
	
	return nil
}

// IncrementPoints increments the daily point counter for a tenant
func (tm *TenantManager) IncrementPoints(tenantID string, points int64) error {
	tenant, err := tm.GetTenant(tenantID)
	if err != nil {
		return err
	}
	
	tenant.mutex.Lock()
	defer tenant.mutex.Unlock()
	
	tenant.CurrentUsage.PointsToday += points
	tenant.CurrentUsage.LastUpdated = time.Now()
	
	return nil
}

// IncrementActiveQueries increments active query counter
func (tm *TenantManager) IncrementActiveQueries(tenantID string) error {
	tenant, err := tm.GetTenant(tenantID)
	if err != nil {
		return err
	}
	
	tenant.mutex.Lock()
	defer tenant.mutex.Unlock()
	
	tenant.CurrentUsage.ActiveQueries++
	
	return nil
}

// DecrementActiveQueries decrements active query counter
func (tm *TenantManager) DecrementActiveQueries(tenantID string) error {
	tenant, err := tm.GetTenant(tenantID)
	if err != nil {
		return err
	}
	
	tenant.mutex.Lock()
	defer tenant.mutex.Unlock()
	
	if tenant.CurrentUsage.ActiveQueries > 0 {
		tenant.CurrentUsage.ActiveQueries--
	}
	
	return nil
}

// ListTenants returns all tenants
func (tm *TenantManager) ListTenants() []*Tenant {
	tm.mutex.RLock()
	defer tm.mutex.RUnlock()
	
	tenants := make([]*Tenant, 0, len(tm.tenants))
	for _, tenant := range tm.tenants {
		tenants = append(tenants, tenant)
	}
	
	return tenants
}

// startResourceMonitoring runs periodic resource usage checks
func (tm *TenantManager) startResourceMonitoring() {
	ticker := time.NewTicker(tm.config.ResourceCheckInterval)
	defer ticker.Stop()
	
	for range ticker.C {
		tm.performResourceCheck()
	}
}

// performResourceCheck checks all tenants for resource violations
func (tm *TenantManager) performResourceCheck() {
	tm.mutex.RLock()
	tenants := make([]*Tenant, 0, len(tm.tenants))
	for _, tenant := range tm.tenants {
		tenants = append(tenants, tenant)
	}
	tm.mutex.RUnlock()
	
	for _, tenant := range tenants {
		tm.checkTenantResourceUsage(tenant)
	}
}

// checkTenantResourceUsage verifies a tenant's resource usage
func (tm *TenantManager) checkTenantResourceUsage(tenant *Tenant) {
	tenant.mutex.RLock()
	usage := tenant.CurrentUsage
	limits := tenant.ResourceLimits
	tenant.mutex.RUnlock()
	
	// Check for violations and take action
	if usage.SeriesCount > limits.MaxSeries {
		tm.handleResourceViolation(tenant.ID, "series", usage.SeriesCount, limits.MaxSeries)
	}
	
	if usage.PointsToday > limits.MaxPointsPerDay {
		tm.handleResourceViolation(tenant.ID, "points", int(usage.PointsToday), int(limits.MaxPointsPerDay))
	}
	
	if usage.StorageUsedGB > float64(limits.MaxStorageGB) {
		tm.handleResourceViolation(tenant.ID, "storage", int(usage.StorageUsedGB), limits.MaxStorageGB)
	}
}

// handleResourceViolation takes action when resource limits are exceeded
func (tm *TenantManager) handleResourceViolation(tenantID, resourceType string, current, limit int) {
	// Log the violation
	fmt.Printf("WARNING: Tenant %s exceeded %s limit (current: %d, limit: %d)\n", 
		tenantID, resourceType, current, limit)
	
	// In a real implementation, you might:
	// - Send alerts to administrators
	// - Throttle the tenant's requests
	// - Suspend the tenant if severe violations
	// - Bill overage charges
}

// TenantContext wraps a context with tenant information
type TenantContext struct {
	context.Context
	TenantID string
}

// NewTenantContext creates a context with tenant information
func NewTenantContext(ctx context.Context, tenantID string) *TenantContext {
	return &TenantContext{
		Context:  ctx,
		TenantID: tenantID,
	}
}

// GetTenantFromContext extracts tenant ID from context
func GetTenantFromContext(ctx context.Context) (string, bool) {
	if tc, ok := ctx.(*TenantContext); ok {
		return tc.TenantID, true
	}
	return "", false
}
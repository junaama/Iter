package contracts

import "github.com/google/uuid"

// OnboardingTenantDomainResponse is returned by GET
// /v1/onboarding/tenant-domain?domain=example.com.
type OnboardingTenantDomainResponse struct {
	Domain string                     `json:"domain"`
	Match  *OnboardingTenantDomainHit `json:"match,omitempty"`
}

type OnboardingTenantDomainHit struct {
	TenantID    uuid.UUID `json:"tenant_id"`
	Name        string    `json:"name"`
	MemberCount int       `json:"member_count"`
}

type OnboardingWorkspaceRequest struct {
	Name string `json:"name"`
}

type OnboardingWorkspaceResponse struct {
	TenantID uuid.UUID `json:"tenant_id"`
	Name     string    `json:"name"`
	Status   string    `json:"status"`
}

type OnboardingTenantJoinRequest struct {
	TenantID uuid.UUID `json:"tenant_id"`
}

type OnboardingTenantJoinResponse struct {
	RequestID  uuid.UUID `json:"request_id"`
	TenantID   uuid.UUID `json:"tenant_id"`
	TenantName string    `json:"tenant_name"`
	Status     string    `json:"status"`
}

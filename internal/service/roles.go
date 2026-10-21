package service

// RoleCodeAdmin matches the "admin" role seeded in the shared roles table
// (back/migrations/pkg/migrations/data/20260705171948_access_roles.sql).
// Access to the support-chat queue is gated on this role, not RoleCodeSupport
// — see docs/superpowers/specs/2026-09-14-chat-service-design.md.
const RoleCodeAdmin = 0

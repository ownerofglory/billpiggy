// Package inbound declares the interfaces HTTP handlers depend on for every
// core service they call.
//
// Each interface lists only the methods an inbound adapter actually calls —
// wiring-only methods (fluent With* setup, EnsureBootstrapSuperAdmin,
// GenerateDue's scheduler entry point) are deliberately excluded, since only
// cmd/billpiggy calls those at startup. A *service.XService already
// implements its corresponding interface here structurally; no adapter or
// wrapper type is needed to satisfy it.
package inbound

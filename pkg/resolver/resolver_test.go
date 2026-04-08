package resolver

// TODO: Add tests for the resolver package. Requires either:
// - A mock GraphQL server (preferred for unit tests)
// - Integration test flag with real GITHUB_TOKEN (for e2e)
//
// Test cases needed:
// - ResolveAll: single action, multiple actions, non-existent repo, non-existent ref
// - ResolveAllRecursive: composite action with nested uses:, depth limiting, cycle detection
// - GraphQL error handling: partial failures, SAML errors, rate limiting
// - action.yml vs action.yaml fallback
// - Deduplication: same NWO@ref from different paths, same NWO from different composite levels

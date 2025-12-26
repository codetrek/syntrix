package authz

import (
	identtypes "github.com/codetrek/syntrix/internal/identity/types"
)

// Reuse public identity rule/request types to avoid duplication.
type (
	RuleSet    = identtypes.RuleSet
	MatchBlock = identtypes.MatchBlock
	Request    = identtypes.AuthzRequest
	Auth       = identtypes.Authenticated
	Resource   = identtypes.Resource
)

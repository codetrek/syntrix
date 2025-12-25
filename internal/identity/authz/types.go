package authz

import "time"

type RuleSet struct {
Version string                `json:"rules_version" yaml:"rules_version"`
Service string                `json:"service" yaml:"service"`
Match   map[string]MatchBlock `json:"match" yaml:"match"`
}

type MatchBlock struct {
Allow map[string]string     `json:"allow" yaml:"allow"`
Match map[string]MatchBlock `json:"match" yaml:"match"`
}

type Request struct {
Auth     Auth      `json:"auth"`
Resource *Resource `json:"resource,omitempty"`
Time     time.Time `json:"time"`
}

type Auth struct {
	UID   interface{}            `json:"uid"`
	Token map[string]interface{} `json:"token"`
}

type Resource struct {
Data map[string]interface{} `json:"data"`
ID   string                 `json:"id"`
}

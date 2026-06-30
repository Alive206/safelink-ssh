package config

import (
	"fmt"
	"net/netip"
	"strings"
)

const (
	ProxyRuleTypeDomain        = "domain"
	ProxyRuleTypeDomainSuffix  = "domain_suffix"
	ProxyRuleTypeDomainKeyword = "domain_keyword"
	ProxyRuleTypeIPCIDR        = "ip_cidr"

	ProxyRuleOutboundProxy  = "selected"
	ProxyRuleOutboundDirect = "direct"
	ProxyRuleOutboundBlock  = "block"
)

// ProxyRule describes one editable rule-mode route entry.
type ProxyRule struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Value    string `json:"value"`
	Outbound string `json:"outbound"`
	Enabled  bool   `json:"enabled"`
}

// DefaultProxyRules returns the built-in rule-mode behavior: keep local and
// private networks direct, and let unmatched traffic use the selected proxy.
func DefaultProxyRules() []ProxyRule {
	return []ProxyRule{
		defaultIPRule("lan-ipv4-this-network", "IPv4 保留地址", "0.0.0.0/8"),
		defaultIPRule("lan-ipv4-private-10", "IPv4 私有地址", "10.0.0.0/8"),
		defaultIPRule("lan-ipv4-carrier", "运营商内网", "100.64.0.0/10"),
		defaultIPRule("lan-ipv4-loopback", "本机地址", "127.0.0.0/8"),
		defaultIPRule("lan-ipv4-link-local", "链路本地", "169.254.0.0/16"),
		defaultIPRule("lan-ipv4-private-172", "IPv4 私有地址", "172.16.0.0/12"),
		defaultIPRule("lan-ipv4-private-192", "IPv4 私有地址", "192.168.0.0/16"),
		defaultIPRule("lan-ipv4-multicast", "组播地址", "224.0.0.0/4"),
		defaultIPRule("lan-ipv6-loopback", "IPv6 本机地址", "::1/128"),
		defaultIPRule("lan-ipv6-unique-local", "IPv6 私有地址", "fc00::/7"),
		defaultIPRule("lan-ipv6-link-local", "IPv6 链路本地", "fe80::/10"),
	}
}

func defaultIPRule(id, name, value string) ProxyRule {
	return ProxyRule{
		ID:       id,
		Name:     name,
		Type:     ProxyRuleTypeIPCIDR,
		Value:    value,
		Outbound: ProxyRuleOutboundDirect,
		Enabled:  true,
	}
}

// NormalizeProxyRules trims user input and falls back unknown types/actions to
// the safest rule-mode defaults.
func NormalizeProxyRules(rules []ProxyRule) []ProxyRule {
	if rules == nil {
		return nil
	}
	normalized := make([]ProxyRule, 0, len(rules))
	for _, rule := range rules {
		rule.ID = strings.TrimSpace(rule.ID)
		rule.Name = strings.TrimSpace(rule.Name)
		rule.Type = NormalizeProxyRuleType(rule.Type)
		rule.Value = strings.TrimSpace(rule.Value)
		rule.Outbound = NormalizeProxyRuleOutbound(rule.Outbound)
		normalized = append(normalized, rule)
	}
	return normalized
}

func NormalizeProxyRuleType(ruleType string) string {
	switch strings.ToLower(strings.TrimSpace(ruleType)) {
	case ProxyRuleTypeDomain:
		return ProxyRuleTypeDomain
	case ProxyRuleTypeDomainKeyword:
		return ProxyRuleTypeDomainKeyword
	case ProxyRuleTypeIPCIDR, "cidr":
		return ProxyRuleTypeIPCIDR
	default:
		return ProxyRuleTypeDomainSuffix
	}
}

func NormalizeProxyRuleOutbound(outbound string) string {
	switch strings.ToLower(strings.TrimSpace(outbound)) {
	case ProxyRuleOutboundDirect:
		return ProxyRuleOutboundDirect
	case ProxyRuleOutboundBlock:
		return ProxyRuleOutboundBlock
	case "proxy", ProxyRuleOutboundProxy:
		return ProxyRuleOutboundProxy
	default:
		return ProxyRuleOutboundDirect
	}
}

// ValidateProxyRules checks enabled rules before they are saved through the UI.
func ValidateProxyRules(rules []ProxyRule) error {
	for index, rule := range NormalizeProxyRules(rules) {
		if !rule.Enabled {
			continue
		}
		if rule.Value == "" {
			return fmt.Errorf("rule %d has an empty match value", index+1)
		}
		if strings.ContainsAny(rule.Value, "\r\n\t ") {
			return fmt.Errorf("rule %d match value contains whitespace", index+1)
		}
		if rule.Type == ProxyRuleTypeIPCIDR {
			if _, err := netip.ParsePrefix(rule.Value); err != nil {
				return fmt.Errorf("rule %d has invalid CIDR %q: %w", index+1, rule.Value, err)
			}
		}
	}
	return nil
}

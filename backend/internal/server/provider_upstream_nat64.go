package server

import (
	"log"
	"net"
	"os"
	"strings"
)

const providerUpstreamNAT64PrefixEnv = "TOKENHUB_PROVIDER_UPSTREAM_NAT64_PREFIX"

var (
	nat64WellKnownPrefix = mustProviderUpstreamCIDR("64:ff9b::/96")
	nat64LocalUsePrefix  = mustProviderUpstreamCIDR("64:ff9b:1::/48")
)

func mustProviderUpstreamCIDR(raw string) *net.IPNet {
	_, block, _ := net.ParseCIDR(raw)
	return block
}

// configuredProviderUpstreamNAT64Prefix reads the RFC 6052 network-specific
// prefix used by the deployment's DNS64/NAT64 gateway. Only the prefix lengths
// defined by RFC 6052 are accepted; an invalid setting keeps the conservative
// default that rejects the RFC 8215 local-use allocation.
func configuredProviderUpstreamNAT64Prefix() (*net.IPNet, int) {
	raw := strings.TrimSpace(os.Getenv("TOKENHUB_PROVIDER_UPSTREAM_NAT64_PREFIX"))
	if raw == "" {
		return nil, 0
	}
	ip, block, err := net.ParseCIDR(raw)
	if err != nil || ip.To4() != nil {
		log.Printf("[tokenhub] ignoring invalid %s value %q", providerUpstreamNAT64PrefixEnv, raw)
		return nil, 0
	}
	ones, bits := block.Mask.Size()
	if bits != 128 || !validRFC6052PrefixLength(ones) {
		log.Printf("[tokenhub] ignoring invalid %s value %q: prefix length must be 32, 40, 48, 56, 64, or 96", providerUpstreamNAT64PrefixEnv, raw)
		return nil, 0
	}
	if ones == 96 && block.IP.To16()[8] != 0 {
		log.Printf("[tokenhub] ignoring invalid %s value %q: RFC 6052 u octet must be zero", providerUpstreamNAT64PrefixEnv, raw)
		return nil, 0
	}
	return block, ones
}

func validRFC6052PrefixLength(bits int) bool {
	switch bits {
	case 32, 40, 48, 56, 64, 96:
		return true
	default:
		return false
	}
}

// providerUpstreamEmbeddedNAT64IPv4 returns the IPv4 target embedded in an
// RFC 6052 address. The bool reports that the address matched a recognized
// prefix; a nil IP with true means the encoding is malformed and must fail
// closed. The well-known /96 prefix works without configuration.
func providerUpstreamEmbeddedNAT64IPv4(ip net.IP) (net.IP, bool) {
	if ip == nil || ip.To4() != nil {
		return nil, false
	}
	bytes := ip.To16()
	if bytes == nil {
		return nil, false
	}
	if nat64WellKnownPrefix.Contains(ip) {
		return net.IPv4(bytes[12], bytes[13], bytes[14], bytes[15]), true
	}
	prefix, prefixBits := configuredProviderUpstreamNAT64Prefix()
	if prefix == nil || !prefix.Contains(ip) {
		return nil, false
	}
	return decodeRFC6052IPv4(bytes, prefixBits), true
}

func decodeRFC6052IPv4(ip []byte, prefixBits int) net.IP {
	if prefixBits == 96 {
		return net.IPv4(ip[12], ip[13], ip[14], ip[15])
	}
	if ip[8] != 0 {
		return nil
	}
	var octets [4]byte
	switch prefixBits {
	case 32:
		copy(octets[:], ip[4:8])
	case 40:
		copy(octets[:3], ip[5:8])
		octets[3] = ip[9]
	case 48:
		copy(octets[:2], ip[6:8])
		copy(octets[2:], ip[9:11])
	case 56:
		octets[0] = ip[7]
		copy(octets[1:], ip[9:12])
	case 64:
		copy(octets[:], ip[9:13])
	default:
		return nil
	}
	return net.IPv4(octets[0], octets[1], octets[2], octets[3])
}

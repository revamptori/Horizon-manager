package core

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
)

type NetInterface struct {
	Name     string `json:"name"`
	IPv4     string `json:"ipv4"`
	Private  bool   `json:"private"`
	Selected bool   `json:"selected"`
}
type NetSnapshot struct {
	Interfaces          []NetInterface `json:"interfaces"`
	SelectedPrivateIPv4 *string        `json:"selectedPrivateIpv4"`
	RecommendedPort     int            `json:"recommendedPort"`
}

func isRFC1918(ip netip.Addr) bool { return ip.Is4() && ip.IsPrivate() }
func routeIPv4() string {
	conn, err := net.Dial("udp4", "1.1.1.1:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	ip, err := netip.ParseAddr(conn.LocalAddr().(*net.UDPAddr).IP.String())
	if err == nil && isRFC1918(ip) {
		return ip.String()
	}
	return ""
}
func networkSnapshot() (NetSnapshot, error) {
	result := NetSnapshot{Interfaces: []NetInterface{}, RecommendedPort: 25565}
	interfaces, err := net.Interfaces()
	if err != nil {
		return result, fmt.Errorf("could not inspect network interfaces: %w", err)
	}
	routed := routeIPv4()
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addresses {
			prefix, err := netip.ParsePrefix(addr.String())
			if err != nil {
				continue
			}
			ip := prefix.Addr()
			if !ip.Is4() || ip.IsUnspecified() || ip.IsLoopback() {
				continue
			}
			result.Interfaces = append(result.Interfaces, NetInterface{Name: iface.Name, IPv4: ip.String(), Private: isRFC1918(ip), Selected: ip.String() == routed})
		}
	}
	sort.Slice(result.Interfaces, func(i, j int) bool {
		a, b := result.Interfaces[i], result.Interfaces[j]
		if a.Selected != b.Selected {
			return a.Selected
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.IPv4 < b.IPv4
	})
	for _, iface := range result.Interfaces {
		if iface.Private {
			selected := iface.IPv4
			result.SelectedPrivateIPv4 = &selected
			break
		}
	}
	address := "127.0.0.1"
	if result.SelectedPrivateIPv4 != nil {
		address = *result.SelectedPrivateIPv4
	}
	for port := 25565; port <= 25665; port++ {
		if ok, _ := checkPort(address, port); ok {
			result.RecommendedPort = port
			break
		}
	}
	return result, nil
}
func checkPort(address string, port int) (bool, error) {
	ip, err := netip.ParseAddr(address)
	if err != nil || !ip.Is4() {
		return false, errors.New("binding address must be an IPv4 address")
	}
	if port < 1024 || port > 65535 {
		return false, errors.New("choose a port from 1024 to 65535")
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort(ip.String(), strconv.Itoa(port)))
	if err != nil {
		return false, nil
	}
	listener.Close()
	return true, nil
}
func validateBinding(address string, port int) error {
	ip, err := netip.ParseAddr(address)
	if err != nil || !ip.Is4() {
		return errors.New("binding address must be a detected private IPv4 address or explicitly selected 0.0.0.0")
	}
	if !ip.IsUnspecified() {
		if !isRFC1918(ip) {
			return errors.New("public, loopback, and CGNAT addresses are not accepted; choose a local RFC 1918 address")
		}
		snapshot, err := networkSnapshot()
		if err != nil {
			return err
		}
		local := false
		for _, iface := range snapshot.Interfaces {
			if iface.IPv4 == address {
				local = true
				break
			}
		}
		if !local {
			return errors.New("that private address is no longer assigned to this device; refresh network detection")
		}
	}
	available, err := checkPort(address, port)
	if err != nil {
		return err
	}
	if !available {
		return fmt.Errorf("TCP port %d is already in use or unavailable on %s", port, address)
	}
	return nil
}

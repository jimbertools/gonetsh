package netsh

import (
	"fmt"
	"io"
	"log"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"errors"

	utilexec "k8s.io/utils/exec"
)

var _pipe io.WriteCloser

func ExecuteNetsh(netshcmd string, cmd string) {
	if _pipe == nil {
		executablePath := "netsh"
		_cmd := exec.Command(executablePath)
		_cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		var err error
		_pipe, err = _cmd.StdinPipe()
		//pipeout, err := _cmd.StdoutPipe()
		if err := _cmd.Start(); err != nil {
			log.Println("Cant start netsh")
		}
		if err != nil {
			log.Println("Can't get pipe", err)
		}
	}
	_pipe.Write([]byte(cmd + "\n"))

}

// Interface is an injectable interface for running netsh commands.  Implementations must be goroutine-safe.
type Interface interface {
	// EnsurePortProxyRule checks if the specified redirect exists, if not creates it
	EnsurePortProxyRule(args []string) (bool, error)
	// DeletePortProxyRule deletes the specified portproxy rule.  If the rule did not exist, return error.
	DeletePortProxyRule(args []string) error
	// DeleteIPAddress checks if the specified IP address is present and, if so, deletes it.
	DeleteIPAddress(args []string) error
	// Restore runs `netsh exec` to restore portproxy or addresses using a file.
	// TODO Check if this is required, most likely not
	Restore(args []string) error
	// Get the interface name that has the default gateway
	GetDefaultGatewayIfaceName() (string, error)
	// Get a list of interfaces and addresses
	GetInterfaces() ([]Ipv4Interface, error)
	// Gets an interface by name
	GetInterfaceByName(name string) (Ipv4Interface, error)
	// Gets an interface by ip address in the format a.b.c.d
	GetInterfaceByIP(ipAddr string) (Ipv4Interface, error)
	SetInterfaceIP(iface string, ip string, mask string) error
	// Enable forwarding on the interface (name or index)
	EnableForwarding(iface string) error
	// Set the DNS server for interface
	SetDNSServer(iface string, dns string) error
	SetMetricByInterfaceName(iface string, metric int) error
	DeleteDNSServers(iface string) error
	SetDHCP(iface string) error
	SetMTU(iface string, mtu int) error
	AddStaticRoute(iface string, cidr string, gateway string) error
	EnableFw() error
	// TODO expand with more features, currently only block or allow all traffic on an interface and set direction
	AddFwRule(name string, iface string, action string, direction string, protocol *string, ports *string) error
	RemoveFwRule(name string) error
	RemoveFwRulesStartingWith(name string) error
	DisableIpv6() error
}

const (
	cmdNetsh string = "netsh"
)

// runner implements Interface in terms of exec("netsh").
type runner struct {
	exec utilexec.Interface
}

// Ipv4Interface models IPv4 interface output from: netsh interface ipv4 show addresses
type Ipv4Interface struct {
	Idx                   int
	Name                  string
	InterfaceMetric       int
	DhcpEnabled           bool
	IpAddress             string
	SubnetPrefix          int
	GatewayMetric         int
	DefaultGatewayAddress string
	DNS                   string
}

// New returns a new Interface which will exec netsh.
func New(exec utilexec.Interface) Interface {

	if exec == nil {
		exec = utilexec.New()
	}

	runner := &runner{
		exec: exec,
	}
	return runner
}

func (runner *runner) GetInterfaces() ([]Ipv4Interface, error) {
	interfaces, interfaceError := runner.getIpAddressConfigurations()

	if interfaceError != nil {
		return nil, interfaceError
	}

	indexMap, indexError := runner.getNetworkInterfaceParameters()

	if indexError != nil {
		return nil, indexError
	}

	// zip them up
	for i := 0; i < len(interfaces); i++ {
		name := interfaces[i].Name

		if val, ok := indexMap[name]; ok {
			interfaces[i].Idx = val
		} else {
			return nil, fmt.Errorf("no index found for interface \"%v\"", name)
		}
	}

	return interfaces, nil
}

// GetInterfaces uses the show addresses command and returns a formatted structure
func (runner *runner) getIpAddressConfigurations() ([]Ipv4Interface, error) {
	args := []string{
		"interface", "ipv4", "show", "config",
	}

	output, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput()
	if err != nil {
		return nil, err
	}
	interfacesString := string(output[:])

	outputLines := strings.Split(interfacesString, "\n")
	var interfaces []Ipv4Interface
	var currentInterface Ipv4Interface
	quotedPattern := regexp.MustCompile("\\\"(.*?)\\\"")
	cidrPattern := regexp.MustCompile(`\/(.*?)\ `)

	if err != nil {
		return nil, err
	}

	for _, outputLine := range outputLines {
		if strings.Contains(outputLine, "Configuration for interface") {
			if currentInterface != (Ipv4Interface{}) {
				interfaces = append(interfaces, currentInterface)
			}
			match := quotedPattern.FindStringSubmatch(outputLine)
			currentInterface = Ipv4Interface{
				Name: match[1],
			}
		} else {
			parts := strings.SplitN(outputLine, ":", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			if strings.HasPrefix(key, "DHCP enabled") {
				if value == "Yes" {
					currentInterface.DhcpEnabled = true
				}
			} else if strings.HasPrefix(key, "InterfaceMetric") {
				if val, err := strconv.Atoi(value); err == nil {
					currentInterface.InterfaceMetric = val
				}
			} else if strings.HasPrefix(key, "Gateway Metric") {
				if val, err := strconv.Atoi(value); err == nil {
					currentInterface.GatewayMetric = val
				}
			} else if strings.HasPrefix(key, "Subnet Prefix") {
				match := cidrPattern.FindStringSubmatch(value)
				if val, err := strconv.Atoi(match[1]); err == nil {
					currentInterface.SubnetPrefix = val
				}
			} else if strings.HasPrefix(key, "IP Address") {
				currentInterface.IpAddress = value
			} else if strings.HasPrefix(key, "Default Gateway") {
				currentInterface.DefaultGatewayAddress = value
			} else if strings.HasPrefix(key, "Statically Configured DNS Servers") {
				currentInterface.DNS = value
			}
		}
	}

	// add the last one
	if currentInterface != (Ipv4Interface{}) {
		interfaces = append(interfaces, currentInterface)
	}

	if len(interfaces) == 0 {
		return nil, fmt.Errorf("no interfaces found in netsh output: %v", interfacesString)
	}

	return interfaces, nil
}

func (runner *runner) getNetworkInterfaceParameters() (map[string]int, error) {
	args := []string{
		"interface", "ipv4", "show", "interfaces",
	}

	output, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput()

	if err != nil {
		return nil, err
	}

	// Split output by line
	outputString := string(output[:])
	outputString = strings.TrimSpace(outputString)
	var outputLines = strings.Split(outputString, "\n")

	if len(outputLines) < 3 {
		return nil, errors.New("unexpected netsh output:\n" + outputString)
	}

	// Remove first two lines of header text
	outputLines = outputLines[2:]

	indexMap := make(map[string]int)

	reg := regexp.MustCompile(`\s{2,}`)

	for _, line := range outputLines {

		line = strings.TrimSpace(line)

		// Split the line by two or more whitespace characters, returning all substrings (n < 0)
		splitLine := reg.Split(line, -1)

		name := splitLine[4]
		if idx, err := strconv.Atoi(splitLine[0]); err == nil {
			indexMap[name] = idx
		}

	}

	return indexMap, nil
}

// Enable forwarding on the interface (name or index)
func (runner *runner) EnableForwarding(iface string) error {
	args := []string{
		"int", "ipv4", "set", "int", strconv.Quote(iface), "for=en",
	}
	cmd := strings.Join(args, " ")
	if stdout, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to enable forwarding on [%v], error: %v. cmd: %v. stdout: %v", iface, err.Error(), cmd, string(stdout))
	}

	return nil
}

// add static route
func (runner *runner) AddStaticRoute(iface string, cidr string, gateway string) error {
	args := []string{
		"int", "ipv4", "add", "route", cidr, iface, gateway,
	}
	cmd := strings.Join(args, " ")
	if stdout, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to add static route on [%v], error: %v. cmd: %v. stdout: %v", iface, err.Error(), cmd, string(stdout))
	}
	return nil
}

func (runner *runner) EnableFw() error {
	args := []string{
		"advfirewall", "set", "allprofiles", "state", "on",
	}
	cmd := strings.Join(args, " ")
	if stdout, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to enable windows firewall, error: %v. cmd: %v. stdout: %v", err.Error(), cmd, string(stdout))
	}
	return nil
}

func (runner *runner) AddFwRule(name string, iface string, action string, direction string, protocol *string, ports *string) error {
	args := []string{
		"advfirewall", "firewall", "add", "rule",
		"name=" + strconv.Quote(name),
		"dir=" + strconv.Quote(direction),
		"localip=" + strconv.Quote(iface),
		"action=" + strconv.Quote(action),
	}

	if protocol != nil {
		args = append(args, "protocol="+strconv.Quote(*protocol))
	}

	if ports != nil {
		args = append(args, "localport="+strconv.Quote(*ports))
	}

	cmd := strings.Join(args, " ")
	if stdout, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to add fw rule on [%v], error: %v. cmd: %v. stdout: %v", iface, err.Error(), cmd, string(stdout))
	}

	return nil
}

func (runner *runner) RemoveFwRule(name string) error {
	args := []string{
		"advfirewall", "firewall", "delete", "rule", "name=" + strconv.Quote(name),
	}
	cmd := strings.Join(args, " ")
	if stdout, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to remove fw rule, error: %v. cmd: %v. stdout: %v", err.Error(), cmd, string(stdout))
	}
	return nil
}

func (runner *runner) RemoveFwRulesStartingWith(name string) error {
	args := []string{
		"advfirewall", "firewall", "show", "rule", "name=all",
	}

	cmd := strings.Join(args, " ")

	stdout, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput()

	if err != nil {
		return fmt.Errorf("failed to remove fw rule, error: %v. cmd: %v. stdout: %v", err.Error(), cmd, string(stdout))
	}

	lines := strings.Split(string(stdout), "\n")

	for _, line := range lines {
		if strings.HasPrefix(line, "Rule Name:") {
			fwRule := strings.TrimSpace(strings.TrimPrefix(line, "Rule Name:"))
			if strings.HasPrefix(fwRule, name) {
				if err := runner.RemoveFwRule(fwRule); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// EnsurePortProxyRule checks if the specified redirect exists, if not creates it.
func (runner *runner) EnsurePortProxyRule(args []string) (bool, error) {
	out, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput()

	if err == nil {
		return true, nil
	}
	if ee, ok := err.(utilexec.ExitError); ok {
		// netsh uses exit(0) to indicate a success of the operation,
		// as compared to a malformed commandline, for example.
		if ee.Exited() && ee.ExitStatus() != 0 {
			return false, nil
		}
	}
	return false, fmt.Errorf("error checking portproxy rule: %v: %s", err, out)

}

// DeletePortProxyRule deletes the specified portproxy rule.  If the rule did not exist, return error.
func (runner *runner) DeletePortProxyRule(args []string) error {
	out, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput()

	if err == nil {
		return nil
	}
	if ee, ok := err.(utilexec.ExitError); ok {
		// netsh uses exit(0) to indicate a success of the operation,
		// as compared to a malformed commandline, for example.
		if ee.Exited() && ee.ExitStatus() == 0 {
			return nil
		}
	}
	return fmt.Errorf("error deleting portproxy rule: %v: %s", err, out)
}

// DeleteIPAddress checks if the specified IP address is present and, if so, deletes it.
func (runner *runner) DeleteIPAddress(args []string) error {
	out, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput()

	if err == nil {
		return nil
	}
	if ee, ok := err.(utilexec.ExitError); ok {
		// netsh uses exit(0) to indicate a success of the operation,
		// as compared to a malformed commandline, for example.
		if ee.Exited() && ee.ExitStatus() == 0 {
			return nil
		}
	}
	return fmt.Errorf("error deleting ipv4 address: %v: %s", err, out)
}

func (runner *runner) GetDefaultGatewayIfaceName() (string, error) {
	interfaces, err := runner.GetInterfaces()
	if err != nil {
		return "", err
	}

	for _, iface := range interfaces {
		if iface.DefaultGatewayAddress != "" {
			return iface.Name, nil
		}
	}

	// return "not found"
	return "", fmt.Errorf("default interface not found")
}

func (runner *runner) GetInterfaceByName(name string) (Ipv4Interface, error) {
	interfaces, err := runner.GetInterfaces()
	if err != nil {
		return Ipv4Interface{}, err
	}

	for _, iface := range interfaces {
		if iface.Name == name {
			return iface, nil
		}
	}

	// return "not found"
	return Ipv4Interface{}, fmt.Errorf("Interface not found: %v", name)
}

func (runner *runner) GetInterfaceByIP(ipAddr string) (Ipv4Interface, error) {
	interfaces, err := runner.GetInterfaces()
	if err != nil {
		return Ipv4Interface{}, err
	}

	for _, iface := range interfaces {
		if iface.IpAddress == ipAddr {
			return iface, nil
		}
	}

	// return "not found"
	return Ipv4Interface{}, fmt.Errorf("Interface not found: %v", ipAddr)
}

// set interface ip address using netsh
func (runner *runner) SetInterfaceIP(iface string, ip string, mask string) error {
	args := []string{
		"int", "ipv4", "set", "address", iface, "static", strconv.Quote(ip), strconv.Quote(mask),
	}
	cmd := strings.Join(args, " ")
	log.Print("EXECUTING SetInterfaceIP: ", cmd)
	if stdout, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set ip on [%v], error: %v. cmd: %v. stdout: %v", iface, err.Error(), cmd, string(stdout))
	}

	return nil
}

func (runner *runner) SetDNSServer(iface string, dns string) error {
	args := []string{
		"int", "ipv4", "set", "dns", iface, "static", dns,
	}
	cmd := strings.Join(args, " ")
	if stdout, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set dns on [%v], error: %v. cmd: %v. stdout: %v", iface, err.Error(), cmd, string(stdout))
	}

	return nil
}

func (runner *runner) SetMetricByInterfaceName(iface string, metric int) error {
	args := []string{
		"interface", "ipv4", "set", "interface", iface, "metric=" + strconv.Itoa(metric),
	}
	cmd := strings.Join(args, " ")
	if stdout, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set metric on [%v], error: %v. cmd: %v. stdout: %v", iface, err.Error(), cmd, string(stdout))
	}

	return nil
}

func (runner *runner) DeleteDNSServers(iface string) error {
	args := []string{
		"interface", "ipv4", "delete", "dnsservers", iface, "all",
	}
	cmd := strings.Join(args, " ")
	if stdout, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to delete dns servers on [%v], error: %v. cmd: %v. stdout: %v", iface, err.Error(), cmd, string(stdout))
	}
	return nil
}

func (runner *runner) SetDHCP(iface string) error {
	args := []string{
		"interface", "ipv4", "set", "dnsservers", iface, "dhcp",
	}
	cmd := strings.Join(args, " ")
	if stdout, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set DHCP on [%v], error: %v. cmd: %v. stdout: %v", iface, err.Error(), cmd, string(stdout))
	}
	return nil
}

// set MTU
func (runner *runner) SetMTU(iface string, mtu int) error {
	args := []string{
		"interface", "ipv4", "set", "subinterface", iface, "mtu=" + strconv.Itoa(mtu),
	}
	cmd := strings.Join(args, " ")
	log.Print("EXECUTING SetMTU: ", cmd)
	if stdout, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set proxy server, error: %v. cmd: %v. stdout: %v", err.Error(), cmd, string(stdout))
	}
	return nil
}

// reset proxy server
func (runner *runner) ResetProxyServer() error {
	arg := []string{
		"winhttp", "reset", "proxy",
	}
	cmd := strings.Join(arg, " ")
	if stdout, err := runner.exec.Command(cmdNetsh, arg...).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to reset proxy server, error: %v. cmd: %v. stdout: %v", err.Error(), cmd, string(stdout))
	}
	return nil
}

func (runner *runner) SetProxyServerIe() error {
	args := []string{
		"winhttp", "import", "proxy", "source=ie"}
	cmd := strings.Join(args, " ")
	if stdout, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set proxy server, error: %v. cmd: %v. stdout: %v", err.Error(), cmd, string(stdout))
	}
	return nil
}

func (runner *runner) DisableIpv6() error {
	args := []string{
		"interface", "ipv6", "set", "global", "randomizeidentifiers=disabled", "store=active",
	}
	cmd := strings.Join(args, " ")
	if stdout, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to disable ipv6, error: %v. cmd: %v. stdout: %v", err.Error(), cmd, string(stdout))
	}

	args = []string{
		"interface", "ipv6", "set", "privacy", "state=disabled", "store=active",
	}
	cmd = strings.Join(args, " ")
	if stdout, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to disable ipv6, error: %v. cmd: %v. stdout: %v", err.Error(), cmd, string(stdout))
	}

	args = []string{
		"interface", "ipv6", "set", "global", "address=disabled", "store=active",
	}
	cmd = strings.Join(args, " ")
	if stdout, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to disable ipv6, error: %v. cmd: %v. stdout: %v", err.Error(), cmd, string(stdout))
	}

	args = []string{
		"interface", "ipv6", "set", "teredo", "disable", "store=active",
	}
	cmd = strings.Join(args, " ")
	if stdout, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to disable ipv6, error: %v. cmd: %v. stdout: %v", err.Error(), cmd, string(stdout))
	}

	args = []string{
		"interface", "ipv6", "isatap", "set", "state", "state=disabled", "store=active",
	}
	cmd = strings.Join(args, " ")
	if stdout, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to disable ipv6, error: %v. cmd: %v. stdout: %v", err.Error(), cmd, string(stdout))
	}

	args = []string{
		"interface", "ipv6", "6to4", "set", "state", "state=disabled", "store=active",
	}
	cmd = strings.Join(args, " ")
	if stdout, err := runner.exec.Command(cmdNetsh, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to disable ipv6, error: %v. cmd: %v. stdout: %v", err.Error(), cmd, string(stdout))
	}

	return nil
}

// Restore is part of Interface.
func (runner *runner) Restore(args []string) error {
	return nil
}

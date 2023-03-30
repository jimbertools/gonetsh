package route

import (
	"fmt"
	"strings"

	utilexec "k8s.io/utils/exec"
)

type Interface interface {
	AddRoute(iface string, cidr string, gateway string) error
	AddRoutes(routes []RouteData) error
	DeleteRoute(dst string, mask string) error
	DeleteRoutes(routes []DeleteRouteData) error
}

type runner struct {
	exec utilexec.Interface
}

const (
	cmdRouting string = "route"
)

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

type RouteData struct {
	Dst     string
	Mask    string
	Gateway string
}
type DeleteRouteData struct {
	Dst  string
	Mask string
}

// add static route
func (runner *runner) AddRoute(dst string, mask string, gateway string) error {
	args := []string{
		"ADD", dst, "MASK", mask, gateway,
	}
	cmd := strings.Join(args, " ")
	stdout, err := runner.exec.Command(cmdRouting, args...).CombinedOutput()
	fmt.Print("adding route ", cmd)
	if err != nil || !strings.Contains(string(stdout), "OK!") {
		strErr := ""
		if err != nil {
			strErr = err.Error()
		}
		return fmt.Errorf("failed to add route on, error: %v. cmd: %v. stdout: %v", strErr, cmd, string(stdout))
	}
	return nil
}

// add static route
func (runner *runner) DeleteRoute(dst string, mask string) error {
	args := []string{
		"DELETE", dst, "MASK", mask,
	}
	cmd := strings.Join(args, " ")
	stdout, err := runner.exec.Command(cmdRouting, args...).CombinedOutput()
	if err != nil || !strings.Contains(string(stdout), "OK!") {
		strErr := ""
		if err != nil {
			strErr = err.Error()
		}
		return fmt.Errorf("failed to delete route on, error: %v. cmd: %v. stdout: %v", strErr, cmd, string(stdout))
	}
	return nil
}

// delete multiple routes
func (runner *runner) DeleteRoutes(routes []DeleteRouteData) error {
	errLine := ""
	for _, route := range routes {
		if err := runner.DeleteRoute(route.Dst, route.Mask); err != nil {
			errLine += err.Error() + ";"
		}
	}
	if errLine != "" {
		return fmt.Errorf("some routes could not be deleted, errors: %v", errLine)
	}
	return nil
}

func (runner *runner) AddRoutes(routes []RouteData) error {
	errLine := ""
	for _, route := range routes {
		if err := runner.AddRoute(route.Dst, route.Mask, route.Gateway); err != nil {
			errLine += err.Error() + ";"
		}
		if errLine != "" {
			return fmt.Errorf("some routes could not be added, errors: %v", errLine)
		}
	}
	return nil
}

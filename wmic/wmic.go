package wmic

import (
	"fmt"
	"strings"

	utilexec "k8s.io/utils/exec"
)

type Interface interface {
	GetPhysicalInterfaceNames() ([]string, error)
}

type runner struct {
	exec utilexec.Interface
}

const (
	cmdWmic string = "wmic"
)

// New returns a new Interface which will exec wmic.
func New(exec utilexec.Interface) Interface {

	if exec == nil {
		exec = utilexec.New()
	}

	runner := &runner{
		exec: exec,
	}
	return runner
}

// add static route
func (runner *runner) GetPhysicalInterfaceNames() ([]string, error) {
	// wmic nic where (PhysicalAdapter='TRUE' and NetConnectionStatus=2) and (PNPDeviceID like '%VMBus%' or '%PCI%'") get NetConnectionID
	args := []string{
		"nic", "where", "(PhysicalAdapter='TRUE' and NetConnectionStatus=2) and (PNPDeviceID like '%VMBus%' or PNPDeviceID like '%PCI%')", "get", "NetConnectionID",
	}
	cmd := strings.Join(args, " ")
	stdout, err := runner.exec.Command(cmdWmic, args...).CombinedOutput()
	if err != nil {
		return []string{}, fmt.Errorf("failed to get physicalinterfacenames, error: %v. cmd: %v. stdout: %v", err.Error(), cmd, string(stdout))
	}

	output := strings.TrimSpace(strings.Replace(string(stdout), "NetConnectionID", "", -1))
	interfaceList := strings.Split(output, "\n")

	for i, interfaceName := range interfaceList {
		interfaceList[i] = strings.TrimSpace(interfaceName)
	}

	return interfaceList, nil
}

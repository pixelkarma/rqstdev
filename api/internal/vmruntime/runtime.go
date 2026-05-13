package vmruntime

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"rqstdev/api/internal/config"
	"rqstdev/api/internal/store"
)

type Runtime struct {
	QEMUBinaryPath string
	BaseDomain     string
	VMsDir         string
}

type Files struct {
	RuntimeDir    string
	DiskImagePath string
	PIDFilePath   string
	QMPSocketPath string
	SerialLogPath string
}

func New(cfg config.Config) Runtime {
	return Runtime{
		QEMUBinaryPath: cfg.QEMUBinaryPath,
		BaseDomain:     cfg.BaseDomain,
		VMsDir:         cfg.VMsDir,
	}
}

func (rt Runtime) FilesForVM(vmUUID string) Files {
	dir := filepath.Join(rt.VMsDir, vmUUID)
	return Files{
		RuntimeDir:    dir,
		DiskImagePath: filepath.Join(dir, "disk.qcow2"),
		PIDFilePath:   filepath.Join(dir, "qemu.pid"),
		QMPSocketPath: filepath.Join(dir, "qmp.sock"),
		SerialLogPath: filepath.Join(dir, "serial.log"),
	}
}

func (rt Runtime) PrepareDisk(files Files, templatePath string) error {
	if err := os.MkdirAll(files.RuntimeDir, 0o755); err != nil {
		return fmt.Errorf("create runtime dir: %w", err)
	}
	src, err := os.Open(templatePath)
	if err != nil {
		return fmt.Errorf("open template image: %w", err)
	}
	defer src.Close()

	dest, err := os.OpenFile(files.DiskImagePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create vm disk: %w", err)
	}
	defer dest.Close()

	if _, err := io.Copy(dest, src); err != nil {
		return fmt.Errorf("copy template image: %w", err)
	}
	return nil
}

func (rt Runtime) StartVM(vm store.VM, files Files, cpuCount, memoryMB int) error {
	_ = os.Remove(files.PIDFilePath)
	_ = os.Remove(files.QMPSocketPath)

	nic := fmt.Sprintf(
		"user,hostfwd=tcp::%d-:22,hostfwd=tcp:127.0.0.1:%d-:%d",
		vm.HostSSHPort,
		vm.HostWebPort,
		vm.GuestWebPort,
	)

	args := []string{
		"-m", strconv.Itoa(memoryMB) + "M",
		"-drive", "file=" + files.DiskImagePath + ",format=qcow2",
		"-nic", nic,
		"-boot", "d",
		"-qmp", "unix:" + files.QMPSocketPath + ",server,nowait",
		"-pidfile", files.PIDFilePath,
		"-monitor", "none",
		"-serial", "file:" + files.SerialLogPath,
		"-display", "none",
		"-daemonize",
	}

	if cpuCount > 1 {
		args = append([]string{"-smp", strconv.Itoa(cpuCount)}, args...)
	}

	cmd := exec.Command(rt.QEMUBinaryPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("start qemu: %w: %s", err, string(output))
	}
	return nil
}

func (rt Runtime) StartExistingVM(vm store.VM) error {
	files := Files{
		RuntimeDir:    vm.RuntimeDir,
		DiskImagePath: vm.DiskImagePath,
		PIDFilePath:   vm.PIDFilePath,
		QMPSocketPath: vm.QMPSocketPath,
		SerialLogPath: vm.SerialLogPath,
	}
	return rt.StartVM(vm, files, vm.CPUCount, vm.MemoryMB)
}

func (rt Runtime) PoweroffVM(vm store.VM) error {
	conn, err := net.DialTimeout("unix", vm.QMPSocketPath, 3*time.Second)
	if err != nil {
		return fmt.Errorf("connect qmp socket: %w", err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return fmt.Errorf("set qmp deadline: %w", err)
	}

	reader := bufio.NewReader(conn)
	if _, err := reader.ReadBytes('\n'); err != nil {
		return fmt.Errorf("read qmp greeting: %w", err)
	}
	for _, execute := range []string{"qmp_capabilities", "system_powerdown"} {
		if err := writeQMPCommand(conn, execute); err != nil {
			return err
		}
		if err := readQMPReply(reader, execute); err != nil {
			return err
		}
	}
	return nil
}

func (rt Runtime) KillVM(vm store.VM) error {
	pid, err := readPID(vm.PIDFilePath)
	if err != nil {
		return err
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find qemu process: %w", err)
	}
	if err := process.Signal(syscall.SIGKILL); err != nil {
		return fmt.Errorf("kill qemu process: %w", err)
	}
	return nil
}

func (rt Runtime) WaitForSSHReady(port int, timeout time.Duration) bool {
	return waitForSSHBanner("127.0.0.1:"+strconv.Itoa(port), timeout)
}

func (rt Runtime) WaitForSSHClosed(port int, timeout time.Duration) bool {
	return waitForTCPClosed("127.0.0.1:"+strconv.Itoa(port), timeout)
}

func (rt Runtime) RemoveRuntimeDir(runtimeDir string) error {
	if err := os.RemoveAll(runtimeDir); err != nil {
		return fmt.Errorf("remove runtime dir: %w", err)
	}
	return nil
}

func (rt Runtime) IsVMRunning(vm store.VM) bool {
	pid, err := readPID(vm.PIDFilePath)
	if err != nil {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

func (rt Runtime) StartPublishedPort(vm store.VM, publicPort, guestPort int) error {
	command := fmt.Sprintf("hostfwd_add tcp::%d-:%d", publicPort, guestPort)
	if err := qmpHumanMonitorCommand(vm.QMPSocketPath, command); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return nil
		}
		return fmt.Errorf("add host forward: %w", err)
	}
	return nil
}

func (rt Runtime) StopPublishedPort(vm store.VM, publicPort int) error {
	command := fmt.Sprintf("hostfwd_remove tcp::%d", publicPort)
	if err := qmpHumanMonitorCommand(vm.QMPSocketPath, command); err != nil {
		if strings.Contains(err.Error(), "Could not find rule") {
			return nil
		}
		return fmt.Errorf("remove host forward: %w", err)
	}
	return nil
}

func waitForTCP(address string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		dialTimeout := tcpProbeTimeout(time.Until(deadline))
		conn, err := net.DialTimeout("tcp", address, dialTimeout)
		if err == nil {
			_ = conn.Close()
			return true
		}
		time.Sleep(tcpProbeSleep(time.Until(deadline)))
	}
	return false
}

func waitForSSHBanner(address string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		dialTimeout := tcpProbeTimeout(time.Until(deadline))
		conn, err := net.DialTimeout("tcp", address, dialTimeout)
		if err == nil {
			_ = conn.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
			buffer := make([]byte, 16)
			n, readErr := conn.Read(buffer)
			_ = conn.Close()
			if readErr == nil && strings.HasPrefix(string(buffer[:n]), "SSH-") {
				return true
			}
		}
		time.Sleep(tcpProbeSleep(time.Until(deadline)))
	}
	return false
}

func waitForTCPClosed(address string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		dialTimeout := tcpProbeTimeout(time.Until(deadline))
		conn, err := net.DialTimeout("tcp", address, dialTimeout)
		if err != nil {
			return true
		}
		_ = conn.Close()
		time.Sleep(tcpProbeSleep(time.Until(deadline)))
	}
	return false
}

func tcpProbeTimeout(remaining time.Duration) time.Duration {
	if remaining <= 0 {
		return 100 * time.Millisecond
	}
	if remaining < 500*time.Millisecond {
		return remaining
	}
	return 500 * time.Millisecond
}

func tcpProbeSleep(remaining time.Duration) time.Duration {
	if remaining <= 0 {
		return 0
	}
	if remaining < 250*time.Millisecond {
		return remaining
	}
	return 250 * time.Millisecond
}

func writeQMPCommand(conn net.Conn, execute string) error {
	payload := map[string]any{"execute": execute}
	if err := json.NewEncoder(conn).Encode(payload); err != nil {
		return fmt.Errorf("write qmp %s: %w", execute, err)
	}
	return nil
}

func readQMPReply(reader *bufio.Reader, execute string) error {
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return fmt.Errorf("read qmp %s reply: %w", execute, err)
		}
		var reply map[string]any
		if err := json.Unmarshal(line, &reply); err != nil {
			continue
		}
		if eventName, ok := reply["event"].(string); ok && eventName != "" {
			continue
		}
		if errValue, ok := reply["error"]; ok && errValue != nil {
			return fmt.Errorf("qmp %s failed: %v", execute, errValue)
		}
		if _, ok := reply["return"]; ok {
			return nil
		}
	}
}

func qmpHumanMonitorCommand(socketPath, command string) error {
	conn, err := net.DialTimeout("unix", socketPath, 3*time.Second)
	if err != nil {
		return fmt.Errorf("connect qmp socket: %w", err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return fmt.Errorf("set qmp deadline: %w", err)
	}

	reader := bufio.NewReader(conn)
	if _, err := reader.ReadBytes('\n'); err != nil {
		return fmt.Errorf("read qmp greeting: %w", err)
	}
	if err := writeQMPCommand(conn, "qmp_capabilities"); err != nil {
		return err
	}
	if err := readQMPReply(reader, "qmp_capabilities"); err != nil {
		return err
	}

	payload := map[string]any{
		"execute": "human-monitor-command",
		"arguments": map[string]any{
			"command-line": command,
		},
	}
	if err := json.NewEncoder(conn).Encode(payload); err != nil {
		return fmt.Errorf("write qmp human-monitor-command: %w", err)
	}
	if err := readQMPReply(reader, "human-monitor-command"); err != nil {
		return err
	}
	return nil
}

func readPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, fmt.Errorf("pid file not found")
		}
		return 0, fmt.Errorf("read pid file: %w", err)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return 0, fmt.Errorf("pid file is empty")
	}
	pid, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse pid file: %w", err)
	}
	return pid, nil
}

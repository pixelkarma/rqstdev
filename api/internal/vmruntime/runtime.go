package vmruntime

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"rqstdev/api/internal/config"
	"rqstdev/api/internal/store"
)

type Runtime struct {
	QEMUBinaryPath  string
	BaseDomain      string
	VMsDir          string
	NginxSnippetDir string
	NginxBinaryPath string
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
		QEMUBinaryPath:  cfg.QEMUBinaryPath,
		BaseDomain:      cfg.BaseDomain,
		VMsDir:          cfg.VMsDir,
		NginxSnippetDir: cfg.NginxSnippetDir,
		NginxBinaryPath: cfg.NginxBinaryPath,
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

	netdev := fmt.Sprintf(
		"user,id=net0,hostfwd=tcp:127.0.0.1:%d-:22,hostfwd=tcp:127.0.0.1:%d-:%d",
		vm.HostSSHPort,
		vm.HostWebPort,
		vm.GuestWebPort,
	)

	args := []string{
		"-name", "rqstdev-" + vm.Name,
		"-machine", "accel=kvm:tcg",
		"-cpu", "host",
		"-m", strconv.Itoa(memoryMB),
		"-smp", strconv.Itoa(cpuCount),
		"-qmp", "unix:" + files.QMPSocketPath + ",server,nowait",
		"-drive", "file=" + files.DiskImagePath + ",format=qcow2,if=virtio",
		"-netdev", netdev,
		"-device", "virtio-net-pci,netdev=net0",
		"-pidfile", files.PIDFilePath,
		"-serial", "file:" + files.SerialLogPath,
		"-display", "none",
		"-daemonize",
	}

	cmd := exec.Command(rt.QEMUBinaryPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("start qemu: %w: %s", err, string(output))
	}
	return nil
}

func (rt Runtime) NginxSnippetPath(vmName string) string {
	return filepath.Join(rt.NginxSnippetDir, "rqstdev-"+vmName+".conf")
}

func (rt Runtime) WriteNginxSnippet(vm store.VM) error {
	if err := os.MkdirAll(rt.NginxSnippetDir, 0o755); err != nil {
		return fmt.Errorf("create nginx snippet dir: %w", err)
	}
	content := fmt.Sprintf(`server {
    listen 80;
    server_name %s.%s;

    location / {
        proxy_pass http://127.0.0.1:%d;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
`, vm.Name, rt.BaseDomain, vm.HostWebPort)
	if err := os.WriteFile(rt.NginxSnippetPath(vm.Name), []byte(content), 0o644); err != nil {
		return fmt.Errorf("write nginx snippet: %w", err)
	}
	return nil
}

func (rt Runtime) ReloadNginx() error {
	commands := [][]string{
		{"sudo", "systemctl", "reload", "nginx"},
		{"sudo", rt.NginxBinaryPath, "-s", "reload"},
		{rt.NginxBinaryPath, "-s", "reload"},
	}

	var lastErr error
	for _, parts := range commands {
		cmd := exec.Command(parts[0], parts[1:]...)
		if output, err := cmd.CombinedOutput(); err == nil {
			return nil
		} else {
			lastErr = fmt.Errorf("%s: %s", err, string(output))
		}
	}

	return fmt.Errorf("reload nginx: %w", lastErr)
}

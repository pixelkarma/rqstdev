package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"rqstdev/cli/internal/client"
	"rqstdev/cli/internal/config"
)

func Run(args []string) error {
	return run(os.Stdout, args)
}

func run(w io.Writer, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet("rqstdev", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	account := fs.String("account", "", "override active account for this command")
	if err := fs.Parse(args); err != nil {
		return err
	}

	rest := fs.Args()
	if len(rest) == 0 {
		return printRootHelp(w, cfg.BaseURL, *account)
	}

	api := client.New(cfg.BaseURL, os.Getenv("RQSTDEV_TOKEN"))

	switch rest[0] {
	case "account":
		return handleAccount(w, api, rest[1:], *account)
	case "invites":
		return handleInvites(w, *account)
	case "list":
		return handleList(w, api, *account)
	case "add":
		return printPlaceholder(w, "vm add", *account)
	case "delete":
		return printPlaceholder(w, "vm delete", *account)
	case "poweron":
		return handlePowerAction(w, api, *account, rest[1:], "poweron")
	case "poweroff":
		return handlePowerAction(w, api, *account, rest[1:], "poweroff")
	case "kill":
		return handlePowerAction(w, api, *account, rest[1:], "kill")
	case "ssh":
		return handleSSH(api, *account, rest[1:])
	case "port":
		return handlePort(w, rest[1:], *account)
	case "help":
		return printRootHelp(w, cfg.BaseURL, *account)
	default:
		return fmt.Errorf("unknown command %q", rest[0])
	}
}

func printRootHelp(w io.Writer, baseURL, account string) error {
	_, err := fmt.Fprintf(
		w,
		"rqstdev CLI scaffold\n\nServer: %s\nActive account override: %s\nToken source: RQSTDEV_TOKEN\n\nCommands:\n  account\n  invites\n  list\n  add\n  delete\n  poweron\n  poweroff\n  kill\n  ssh\n  port\n",
		baseURL,
		printable(account),
	)
	return err
}

func handleAccount(w io.Writer, api *client.Client, args []string, account string) error {
	if len(args) == 0 {
		accounts, err := api.ListAccounts()
		if err != nil {
			return err
		}
		if len(accounts) == 0 {
			_, err := fmt.Fprintln(w, "No accounts.")
			return err
		}
		for _, item := range accounts {
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\n", item.Name, item.Role, item.UUID); err != nil {
				return err
			}
		}
		return nil
	}

	switch args[0] {
	case "create":
		if len(args) < 2 {
			return errors.New("account create requires a name")
		}
		name := strings.TrimSpace(strings.Join(args[1:], " "))
		account, err := api.CreateAccount(name)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "Created account %q (%s)\n", account.Name, account.UUID)
		return err
	case "add", "remove", "default", "use", "transfer":
		return printPlaceholder(w, "account "+args[0], account)
	case "user":
		if len(args) < 2 {
			return errors.New("account user requires a subcommand")
		}
		switch args[1] {
		case "invite", "revoke":
			return printPlaceholder(w, "account user "+args[1], account)
		default:
			return fmt.Errorf("unknown account user subcommand %q", args[1])
		}
	default:
		return fmt.Errorf("unknown account subcommand %q", args[0])
	}
}

func handleInvites(w io.Writer, account string) error {
	return printPlaceholder(w, "invites", account)
}

func handleList(w io.Writer, api *client.Client, accountHint string) error {
	account, err := resolveAccount(api, accountHint)
	if err != nil {
		return err
	}
	vms, err := api.ListVMs(account.UUID)
	if err != nil {
		return err
	}
	if len(vms) == 0 {
		_, err := fmt.Fprintf(w, "No VMs in account %q.\n", account.Name)
		return err
	}
	for _, vm := range vms {
		if _, err := fmt.Fprintf(w, "%s\t%s\tssh=%t\tweb=%t\tssh-port=%d\tweb-port=%d\n", vm.Name, vm.State, vm.SSHReady, vm.WebReady, vm.HostSSHPort, vm.HostWebPort); err != nil {
			return err
		}
		if vm.LastError != "" {
			if _, err := fmt.Fprintf(w, "  error: %s\n", vm.LastError); err != nil {
				return err
			}
		}
	}
	return nil
}

func handlePowerAction(w io.Writer, api *client.Client, accountHint string, args []string, action string) error {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return fmt.Errorf("%s requires a VM name", action)
	}
	account, err := resolveAccount(api, accountHint)
	if err != nil {
		return err
	}
	vmName := strings.TrimSpace(args[0])

	var vm client.VM
	switch action {
	case "poweron":
		vm, err = api.PoweronVM(account.UUID, vmName)
	case "poweroff":
		vm, err = api.PoweroffVM(account.UUID, vmName)
	case "kill":
		vm, err = api.KillVM(account.UUID, vmName)
	default:
		return fmt.Errorf("unknown power action %q", action)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\t%s\tssh=%t\tweb=%t\n", vm.Name, vm.State, vm.SSHReady, vm.WebReady)
	return err
}

func handleSSH(api *client.Client, accountHint string, args []string) error {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return errors.New("ssh requires a VM name")
	}
	account, err := resolveAccount(api, accountHint)
	if err != nil {
		return err
	}
	username, vmName := parseSSHVMTarget(args[0])
	if vmName == "" {
		return errors.New("ssh requires a VM name")
	}
	resolution, err := api.ResolveVM(account.UUID, vmName)
	if err != nil {
		return err
	}
	if !resolution.SSH.Ready {
		return fmt.Errorf("vm %q is not SSH-ready yet", vmName)
	}
	return client.RunSSH(resolution, username)
}

func handlePort(w io.Writer, args []string, account string) error {
	if len(args) == 0 {
		return errors.New("port requires a subcommand")
	}

	switch args[0] {
	case "add", "remove", "list":
		return printPlaceholder(w, "port "+args[0], account)
	default:
		return fmt.Errorf("unknown port subcommand %q", args[0])
	}
}

func printPlaceholder(w io.Writer, area, account string) error {
	_, err := fmt.Fprintf(w, "%s is not implemented yet (account=%s)\n", area, printable(account))
	return err
}

func printable(value string) string {
	if value == "" {
		return "<default>"
	}
	return value
}

func resolveAccount(api *client.Client, hint string) (client.Account, error) {
	accounts, err := api.ListAccounts()
	if err != nil {
		return client.Account{}, err
	}
	if len(accounts) == 0 {
		return client.Account{}, errors.New("no accounts available")
	}
	hint = strings.TrimSpace(hint)
	if hint == "" {
		if len(accounts) == 1 {
			return accounts[0], nil
		}
		return client.Account{}, errors.New("multiple accounts available; use --account")
	}
	for _, account := range accounts {
		if account.UUID == hint || strings.EqualFold(account.Name, hint) {
			return account, nil
		}
	}
	return client.Account{}, fmt.Errorf("account %q not found", hint)
}

func parseSSHVMTarget(value string) (username string, vmName string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	parts := strings.SplitN(value, "@", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	return "", value
}

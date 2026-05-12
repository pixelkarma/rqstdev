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
		return printPlaceholder(w, "vm list", *account)
	case "add":
		return printPlaceholder(w, "vm add", *account)
	case "delete":
		return printPlaceholder(w, "vm delete", *account)
	case "poweron":
		return printPlaceholder(w, "vm poweron", *account)
	case "poweroff":
		return printPlaceholder(w, "vm poweroff", *account)
	case "kill":
		return printPlaceholder(w, "vm kill", *account)
	case "ssh":
		return printPlaceholder(w, "vm ssh", *account)
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

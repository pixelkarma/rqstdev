package app

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"

	"rqstdev/cli/internal/client"
	"rqstdev/cli/internal/config"
	"rqstdev/cli/internal/secret"
	"rqstdev/cli/internal/session"
	"rqstdev/cli/internal/sshcopy"
	"rqstdev/cli/internal/tui"
)

func Run(args []string) error {
	return run(os.Stdout, args)
}

func run(w io.Writer, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	sess, err := session.Load()
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet("rqstdev", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	accountHint := fs.String("account", "", "override active account for this command")
	if err := fs.Parse(args); err != nil {
		return err
	}

	rest := fs.Args()
	token := strings.TrimSpace(os.Getenv("RQSTDEV_TOKEN"))
	if token == "" {
		token, err = secret.LoadToken(cfg.BaseURL)
		if err != nil {
			return err
		}
	}
	if len(rest) == 0 {
		if token == "" {
			return handleUnauthenticatedRoot(w, &cfg)
		}
		api := client.New(cfg.BaseURL, token)
		return handleAuthenticatedRoot(w, cfg, sess, api)
	}
	api := client.New(cfg.BaseURL, token)

	switch rest[0] {
	case "signup":
		return handleSignup(w, &cfg)
	case "login":
		return handleLogin(w, &cfg)
	case "logout":
		return handleLogout(w, cfg, api)
	case "account":
		return handleAccount(w, &cfg, &sess, api, rest[1:], *accountHint)
	case "invites":
		return handleInvites(w, cfg, api, rest[1:])
	case "list":
		return handleList(w, cfg, sess, api, *accountHint)
	case "add":
		return handleAdd(w, cfg, sess, api, *accountHint, rest[1:])
	case "delete":
		return handleDelete(w, cfg, sess, api, *accountHint, rest[1:])
	case "poweron":
		return handlePowerAction(w, cfg, sess, api, *accountHint, rest[1:], "poweron")
	case "poweroff":
		return handlePowerAction(w, cfg, sess, api, *accountHint, rest[1:], "poweroff")
	case "kill":
		return handlePowerAction(w, cfg, sess, api, *accountHint, rest[1:], "kill")
	case "ssh":
		return handleSSH(cfg, sess, api, *accountHint, rest[1:])
	case "ssh-copy-id":
		return handleSSHCopyID(w, cfg, sess, api, *accountHint, rest[1:])
	case "port":
		return handlePort(w, cfg, sess, api, *accountHint, rest[1:])
	case "help":
		return printRootHelp(w, cfg, sess)
	default:
		return fmt.Errorf("unknown command %q", rest[0])
	}
}

func handleAuthenticatedRoot(w io.Writer, cfg config.Config, sess session.State, api *client.Client) error {
	user, accounts, err := api.Me()
	if err != nil {
		return handleClientError(cfg.BaseURL, err)
	}
	if err := syncAccounts(&cfg, accounts); err != nil {
		return err
	}
	if err := config.Save(cfg); err != nil {
		return err
	}
	account, accountErr := resolveAccount(cfg, sess, api, "")
	invites, inviteErr := api.ListInvites()
	lines := []string{
		"Server: " + cfg.BaseURL,
		"User: " + user.Email,
	}
	if accountErr == nil {
		lines = append(lines, "Current account: "+account.Name)
	} else {
		lines = append(lines, "Current account: <none>")
	}
	if inviteErr == nil {
		lines = append(lines, fmt.Sprintf("Pending invites: %d", len(invites)))
	}
	if accountErr == nil {
		vms, err := api.ListVMs(account.UUID)
		if err == nil {
			lines = append(lines, fmt.Sprintf("VMs in current account: %d", len(vms)))
			for _, vm := range vms {
				lines = append(lines, fmt.Sprintf("  %s\t%s\ttemplate=%s\tcpu=%d\tram=%dMB", vm.Name, vm.State, vm.TemplateName, vm.CPUCount, vm.MemoryMB))
			}
		}
	}
	if !tui.CanUse() {
		if _, err := fmt.Fprintln(w, "rqstdev CLI\n"); err != nil {
			return err
		}
		for _, line := range lines {
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
		}
		_, err = fmt.Fprintln(w, "\nCommands:\n  signup\n  login\n  logout\n  account\n  invites\n  list\n  add\n  delete\n  poweron\n  poweroff\n  kill\n  ssh\n  ssh-copy-id\n  port")
		return err
	}
	options := []tui.Option{
		{Label: "List VMs"},
		{Label: "Add VM"},
		{Label: "SSH to VM"},
		{Label: "Manage Invites"},
		{Label: "Account List"},
		{Label: "Logout"},
		{Label: "Help"},
		{Label: "Quit"},
	}
	choice, err := tui.Select("rqstdev", lines, options, 0)
	if err != nil {
		if errors.Is(err, tui.ErrAborted) {
			return nil
		}
		return err
	}
	switch options[choice].Label {
	case "List VMs":
		return handleList(w, cfg, sess, api, "")
	case "Add VM":
		return handleAdd(w, cfg, sess, api, "", nil)
	case "SSH to VM":
		return handleSSH(cfg, sess, api, "", nil)
	case "Manage Invites":
		return handleInvites(w, cfg, api, nil)
	case "Account List":
		return handleAccount(w, &cfg, &sess, api, nil, "")
	case "Logout":
		return handleLogout(w, cfg, api)
	case "Help":
		return printRootHelp(w, cfg, sess)
	default:
		return nil
	}
}

func printRootHelp(w io.Writer, cfg config.Config, sess session.State) error {
	_, err := fmt.Fprintf(
		w,
		"rqstdev CLI\n\nServer: %s\nCurrent account: %s\n\nCommands:\n  signup\n  login\n  logout\n  account\n  invites\n  list\n  add\n  delete\n  poweron\n  poweroff\n  kill\n  ssh\n  ssh-copy-id\n  port\n",
		cfg.BaseURL,
		currentAccountLabel(cfg, sess),
	)
	return err
}

func handleUnauthenticatedRoot(w io.Writer, cfg *config.Config) error {
	choice := "login"
	if tui.CanUse() {
		index, err := tui.Select("rqstdev", []string{"No stored token found."}, []tui.Option{
			{Label: "Login"},
			{Label: "Signup"},
			{Label: "Quit"},
		}, 0)
		if err != nil {
			if errors.Is(err, tui.ErrAborted) {
				return nil
			}
			return err
		}
		choice = strings.ToLower([]string{"login", "signup", "quit"}[index])
	} else {
		reader := bufio.NewReader(os.Stdin)
		value, err := prompt(reader, "Not authenticated. Choose login or signup", "login")
		if err != nil {
			return err
		}
		choice = strings.ToLower(strings.TrimSpace(value))
	}
	switch choice {
	case "", "login":
		return handleLogin(w, cfg)
	case "signup":
		return handleSignup(w, cfg)
	case "quit":
		return nil
	default:
		return fmt.Errorf("unknown choice %q", choice)
	}
}

func handleSignup(w io.Writer, cfg *config.Config) error {
	email, password, accountName, err := signupInputs(cfg.LastEmail)
	if err != nil {
		return err
	}
	api := client.New(cfg.BaseURL, "")
	result, err := api.Signup(email, password, accountName)
	if err != nil {
		return err
	}
	return finalizeAuthResult(w, cfg, api, result)
}

func handleLogin(w io.Writer, cfg *config.Config) error {
	email, password, err := loginInputs(cfg.LastEmail)
	if err != nil {
		return err
	}
	api := client.New(cfg.BaseURL, "")
	result, err := api.Login(email, password)
	if err != nil {
		return err
	}
	return finalizeAuthResult(w, cfg, api, result)
}

func handleLogout(w io.Writer, cfg config.Config, api *client.Client) error {
	if err := api.Logout(); err != nil {
		if wrapped := handleClientError(cfg.BaseURL, err); wrapped != nil {
			return wrapped
		}
	}
	if err := secret.DeleteToken(cfg.BaseURL); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w, "Logged out.")
	return err
}

func handleAccount(w io.Writer, cfg *config.Config, sess *session.State, api *client.Client, args []string, accountHint string) error {
	if len(args) == 0 {
		accounts, err := api.ListAccounts()
		if err != nil {
			return handleClientError(cfg.BaseURL, err)
		}
		if len(accounts) == 0 {
			_, err := fmt.Fprintln(w, "No accounts.")
			return err
		}
		if err := syncAccounts(cfg, accounts); err != nil {
			return err
		}
		if err := config.Save(*cfg); err != nil {
			return err
		}
		for _, item := range accounts {
			marker := localAccountMarker(*cfg, *sess, item.UUID)
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s%s\n", item.Name, item.Role, item.UUID, marker); err != nil {
				return err
			}
		}
		return nil
	}

	switch args[0] {
	case "create":
		if len(args) < 2 {
			if tui.CanUse() {
				values, err := tui.Inputs("Create Account", nil, []tui.Field{{
					Key:   "name",
					Label: "Account name",
					Value: "My Account",
				}})
				if err != nil {
					return err
				}
				args = append(args, values["name"])
			} else {
				return errors.New("account create requires a name")
			}
		}
		account, err := api.CreateAccount(strings.TrimSpace(strings.Join(args[1:], " ")))
		if err != nil {
			return handleClientError(cfg.BaseURL, err)
		}
		cfg.UpsertAccount(config.AccountRef{
			UUID:    account.UUID,
			Name:    account.Name,
			BaseURL: cfg.BaseURL,
		})
		if cfg.DefaultAccount == "" {
			if alias, _, ok := cfg.ResolveLocalAccount(account.UUID); ok {
				cfg.DefaultAccount = alias
			}
		}
		if err := config.Save(*cfg); err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "Created account %q (%s)\n", account.Name, account.UUID)
		return err
	case "add":
		return handleAccountAdd(w, cfg, api, args[1:])
	case "remove":
		return handleAccountRemove(w, cfg, sess, args[1:])
	case "default":
		return handleAccountDefault(w, cfg, args[1:])
	case "use":
		return handleAccountUse(w, cfg, sess, args[1:])
	case "transfer":
		return handleAccountTransfer(w, cfg, sess, api, accountHint, args[1:])
	case "forgot":
		return handleForgot(w, cfg, args[1:])
	case "user":
		if len(args) < 2 {
			return errors.New("account user requires a subcommand")
		}
		switch args[1] {
		case "invite":
			return handleAccountInvite(w, *cfg, *sess, api, accountHint, args[2:])
		case "revoke":
			return handleAccountRevoke(w, *cfg, *sess, api, accountHint, args[2:])
		default:
			return fmt.Errorf("unknown account user subcommand %q", args[1])
		}
	default:
		return fmt.Errorf("unknown account subcommand %q", args[0])
	}
}

func handleAccountAdd(w io.Writer, cfg *config.Config, api *client.Client, args []string) error {
	accounts, err := api.ListAccounts()
	if err != nil {
		return handleClientError(cfg.BaseURL, err)
	}
	var target client.Account
	if len(args) == 0 {
		target, err = selectRemoteAccount(accounts)
		if err != nil {
			return err
		}
	} else {
		target, err = resolveRemoteAccount(accounts, args[0])
	}
	if err != nil {
		return err
	}
	alias := ""
	if len(args) > 1 {
		alias = strings.TrimSpace(strings.Join(args[1:], " "))
	}
	cfg.UpsertAccount(config.AccountRef{
		Alias:   alias,
		UUID:    target.UUID,
		Name:    target.Name,
		BaseURL: cfg.BaseURL,
	})
	if err := config.Save(*cfg); err != nil {
		return err
	}
	resolvedAlias, _, _ := cfg.ResolveLocalAccount(target.UUID)
	_, err = fmt.Fprintf(w, "Added local account alias %q for %s\n", resolvedAlias, target.UUID)
	return err
}

func handleAccountRemove(w io.Writer, cfg *config.Config, sess *session.State, args []string) error {
	if len(args) == 0 && tui.CanUse() {
		ref, err := selectLocalAccount(*cfg)
		if err != nil {
			return err
		}
		args = append(args, ref.Alias)
	}
	if len(args) == 0 {
		return errors.New("account remove requires an alias, account name, or account UUID")
	}
	alias, _, ok := cfg.ResolveLocalAccount(args[0])
	if !ok {
		return fmt.Errorf("account %q is not configured locally", args[0])
	}
	if !cfg.RemoveAccount(alias) {
		return fmt.Errorf("account %q is not configured locally", args[0])
	}
	if sess.ActiveAccount == alias {
		sess.ActiveAccount = ""
		if err := session.Clear(); err != nil {
			return err
		}
	}
	if err := config.Save(*cfg); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "Removed local account alias %q\n", alias)
	return err
}

func handleAccountDefault(w io.Writer, cfg *config.Config, args []string) error {
	if len(args) == 0 && tui.CanUse() {
		ref, err := selectLocalAccount(*cfg)
		if err != nil {
			return err
		}
		args = append(args, ref.Alias)
	}
	if len(args) == 0 {
		return errors.New("account default requires an alias, account name, or account UUID")
	}
	alias, _, ok := cfg.ResolveLocalAccount(args[0])
	if !ok {
		return fmt.Errorf("account %q is not configured locally", args[0])
	}
	cfg.DefaultAccount = alias
	if err := config.Save(*cfg); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "Default account set to %q\n", alias)
	return err
}

func handleAccountUse(w io.Writer, cfg *config.Config, sess *session.State, args []string) error {
	if len(args) == 0 && tui.CanUse() {
		ref, err := selectLocalAccount(*cfg)
		if err != nil {
			return err
		}
		args = append(args, ref.Alias)
	}
	if len(args) == 0 {
		return errors.New("account use requires an alias, account name, or account UUID")
	}
	alias, _, ok := cfg.ResolveLocalAccount(args[0])
	if !ok {
		return fmt.Errorf("account %q is not configured locally", args[0])
	}
	sess.ActiveAccount = alias
	if err := session.Save(*sess); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "Session account set to %q\n", alias)
	return err
}

func handleForgot(w io.Writer, cfg *config.Config, args []string) error {
	if len(args) == 0 {
		return errors.New("account forgot requires an email")
	}
	email := strings.TrimSpace(args[0])
	api := client.New(cfg.BaseURL, "")
	if err := api.Forgot(email); err != nil {
		return err
	}
	code, newPassword, confirm, err := forgotInputs(email)
	if err != nil {
		return err
	}
	if newPassword != confirm {
		return errors.New("passwords do not match")
	}
	if err := api.Reset(email, code, newPassword); err != nil {
		return err
	}
	if err := secret.DeleteToken(cfg.BaseURL); err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, "Password reset complete. Please log in again.")
	return err
}

func handleAccountInvite(w io.Writer, cfg config.Config, sess session.State, api *client.Client, accountHint string, args []string) error {
	if len(args) == 0 {
		return errors.New("account user invite requires an email")
	}
	account, err := resolveAccount(cfg, sess, api, accountHint)
	if err != nil {
		return err
	}
	role := "user"
	if len(args) > 1 && strings.TrimSpace(args[1]) != "" {
		role = strings.TrimSpace(args[1])
	}
	invite, err := api.InviteUser(account.UUID, args[0], role)
	if err != nil {
		return handleClientError(cfg.BaseURL, err)
	}
	_, err = fmt.Fprintf(w, "Invited %s to %s as %s (%s)\n", args[0], invite.AccountName, invite.Role, invite.UUID)
	return err
}

func handleAccountRevoke(w io.Writer, cfg config.Config, sess session.State, api *client.Client, accountHint string, args []string) error {
	if len(args) == 0 {
		return errors.New("account user revoke requires an email or user UUID")
	}
	account, err := resolveAccount(cfg, sess, api, accountHint)
	if err != nil {
		return err
	}
	_, members, err := api.AccountDetails(account.UUID)
	if err != nil {
		return handleClientError(cfg.BaseURL, err)
	}
	targetUUID, err := resolveMember(members, args[0])
	if err != nil {
		return err
	}
	if err := api.RevokeUser(account.UUID, targetUUID); err != nil {
		return handleClientError(cfg.BaseURL, err)
	}
	_, err = fmt.Fprintf(w, "Revoked %s from %s\n", args[0], account.Name)
	return err
}

func handleAccountTransfer(w io.Writer, cfg *config.Config, sess *session.State, api *client.Client, accountHint string, args []string) error {
	if len(args) == 0 {
		return errors.New("account transfer requires an email")
	}
	account, err := resolveAccount(*cfg, *sess, api, accountHint)
	if err != nil {
		return err
	}
	if err := api.TransferAccount(account.UUID, args[0]); err != nil {
		return handleClientError(cfg.BaseURL, err)
	}
	_, _ = fmt.Fprintf(w, "Transferred ownership of %s to %s\n", account.Name, args[0])

	createNew, name, err := transferFollowup()
	if err != nil {
		return err
	}
	if createNew {
		created, err := api.CreateAccount(name)
		if err != nil {
			return handleClientError(cfg.BaseURL, err)
		}
		cfg.UpsertAccount(config.AccountRef{UUID: created.UUID, Name: created.Name, BaseURL: cfg.BaseURL})
		if err := config.Save(*cfg); err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "Created account %q (%s)\n", created.Name, created.UUID)
		return err
	}
	return nil
}

func handleInvites(w io.Writer, cfg config.Config, api *client.Client, args []string) error {
	if len(args) == 0 {
		invites, err := api.ListInvites()
		if err != nil {
			return handleClientError(cfg.BaseURL, err)
		}
		if len(invites) == 0 {
			_, err := fmt.Fprintln(w, "No pending invites.")
			return err
		}
		if tui.CanUse() {
			invite, action, err := selectInviteAction(invites)
			if err != nil {
				return err
			}
			switch action {
			case "accept":
				err = api.AcceptInvite(invite.UUID)
			case "refuse":
				err = api.RefuseInvite(invite.UUID)
			default:
				return nil
			}
			if err != nil {
				return handleClientError(cfg.BaseURL, err)
			}
			_, err = fmt.Fprintf(w, "Invite %s %sed\n", invite.UUID, action)
			return err
		}
		for i, invite := range invites {
			if _, err := fmt.Fprintf(w, "%d.\t%s\t%s\t%s\t%s\n", i+1, invite.UUID, invite.AccountName, invite.Role, invite.CreatedAt); err != nil {
				return err
			}
		}
		reader := bufio.NewReader(os.Stdin)
		selection, err := prompt(reader, "Invite number to accept/refuse (blank to exit)", "")
		if err != nil {
			return err
		}
		if strings.TrimSpace(selection) == "" {
			return nil
		}
		index, err := strconv.Atoi(strings.TrimSpace(selection))
		if err != nil || index < 1 || index > len(invites) {
			return errors.New("invalid invite selection")
		}
		action, err := prompt(reader, "Action accept/refuse", "accept")
		if err != nil {
			return err
		}
		switch strings.ToLower(strings.TrimSpace(action)) {
		case "accept":
			err = api.AcceptInvite(invites[index-1].UUID)
		case "refuse":
			err = api.RefuseInvite(invites[index-1].UUID)
		default:
			return fmt.Errorf("unknown invite action %q", action)
		}
		if err != nil {
			return handleClientError(cfg.BaseURL, err)
		}
		_, err = fmt.Fprintf(w, "Invite %s %sed\n", invites[index-1].UUID, strings.ToLower(strings.TrimSpace(action)))
		return err
	}
	if len(args) < 2 {
		return errors.New("invites accept|refuse requires an invite UUID")
	}
	var err error
	switch args[0] {
	case "accept":
		err = api.AcceptInvite(args[1])
	case "refuse":
		err = api.RefuseInvite(args[1])
	default:
		return fmt.Errorf("unknown invites subcommand %q", args[0])
	}
	if err != nil {
		return handleClientError(cfg.BaseURL, err)
	}
	_, err = fmt.Fprintf(w, "Invite %s %sed\n", args[1], args[0])
	return err
}

func handleList(w io.Writer, cfg config.Config, sess session.State, api *client.Client, accountHint string) error {
	account, err := resolveAccount(cfg, sess, api, accountHint)
	if err != nil {
		return err
	}
	vms, err := api.ListVMs(account.UUID)
	if err != nil {
		return handleClientError(cfg.BaseURL, err)
	}
	if len(vms) == 0 {
		_, err := fmt.Fprintf(w, "No VMs in account %q.\n", account.Name)
		return err
	}
	for _, vm := range vms {
		if _, err := fmt.Fprintf(w, "%s\t%s\ttemplate=%s\tcpu=%d\tram=%dMB\n", vm.Name, vm.State, vm.TemplateName, vm.CPUCount, vm.MemoryMB); err != nil {
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

func handleAdd(w io.Writer, cfg config.Config, sess session.State, api *client.Client, accountHint string, args []string) error {
	account, err := resolveAccount(cfg, sess, api, accountHint)
	if err != nil {
		return err
	}
	vmName := strings.TrimSpace(firstArg(args))
	guestPortArg := ""
	if len(args) > 1 {
		guestPortArg = strings.TrimSpace(args[1])
	}
	if vmName == "" || guestPortArg == "" {
		vmName, guestPortArg, err = addInputs(vmName, guestPortArg)
		if err != nil {
			return err
		}
	}
	guestPort := 80
	if portValue := strings.TrimSpace(guestPortArg); portValue != "" {
		if guestPort, err = strconv.Atoi(portValue); err != nil {
			return errors.New("guest web port must be numeric")
		}
	}
	vm, err := api.CreateVM(account.UUID, vmName, guestPort)
	if err != nil {
		return handleClientError(cfg.BaseURL, err)
	}
	_, err = fmt.Fprintf(w, "Created VM %s (%s) state=%s template=%s cpu=%d ram=%dMB\n", vm.Name, vm.UUID, vm.State, vm.TemplateName, vm.CPUCount, vm.MemoryMB)
	return err
}

func handleDelete(w io.Writer, cfg config.Config, sess session.State, api *client.Client, accountHint string, args []string) error {
	account, err := resolveAccount(cfg, sess, api, accountHint)
	if err != nil {
		return err
	}
	vmName, remaining, err := resolveVMArgument(api, account.UUID, args)
	if err != nil {
		return err
	}
	force := hasFlag(remaining, "--force")
	if !force {
		if err := deleteConfirmation(vmName); err != nil {
			return errors.New("delete cancelled")
		}
	}
	if err := api.DeleteVM(account.UUID, vmName); err != nil {
		return handleClientError(cfg.BaseURL, err)
	}
	_, err = fmt.Fprintf(w, "Deleted VM %s\n", vmName)
	return err
}

func handlePowerAction(w io.Writer, cfg config.Config, sess session.State, api *client.Client, accountHint string, args []string, action string) error {
	account, err := resolveAccount(cfg, sess, api, accountHint)
	if err != nil {
		return err
	}
	vmName, _, err := resolveVMArgument(api, account.UUID, args)
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}

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
		return handleClientError(cfg.BaseURL, err)
	}
	_, err = fmt.Fprintf(w, "%s\t%s\ttemplate=%s\tcpu=%d\tram=%dMB\n", vm.Name, vm.State, vm.TemplateName, vm.CPUCount, vm.MemoryMB)
	return err
}

func handleSSH(cfg config.Config, sess session.State, api *client.Client, accountHint string, args []string) error {
	account, err := resolveAccount(cfg, sess, api, accountHint)
	if err != nil {
		return err
	}
	target := ""
	if len(args) > 0 {
		target = args[0]
	}
	username, vmName := parseSSHVMTarget(target)
	if vmName == "" {
		vmName, _, err = resolveVMArgument(api, account.UUID, nil)
		if err != nil {
			return err
		}
	}
	resolution, err := api.ResolveVM(account.UUID, vmName)
	if err != nil {
		return handleClientError(cfg.BaseURL, err)
	}
	if !resolution.SSH.Ready {
		return fmt.Errorf("vm %q is not SSH-ready yet", vmName)
	}
	return client.RunSSH(resolution, username)
}

func handleSSHCopyID(w io.Writer, cfg config.Config, sess session.State, api *client.Client, accountHint string, args []string) error {
	account, err := resolveAccount(cfg, sess, api, accountHint)
	if err != nil {
		return err
	}
	target := ""
	if len(args) > 0 {
		target = args[0]
	}
	username, vmName := parseSSHVMTarget(target)
	if vmName == "" {
		vmName, _, err = resolveVMArgument(api, account.UUID, nil)
		if err != nil {
			return err
		}
	}
	if strings.TrimSpace(username) == "" {
		username = "root"
	}
	resolution, err := api.ResolveVM(account.UUID, vmName)
	if err != nil {
		return handleClientError(cfg.BaseURL, err)
	}
	if !resolution.SSH.Ready {
		return fmt.Errorf("vm %q is not SSH-ready yet", vmName)
	}

	keyPath, err := selectPublicKeyPath()
	if err != nil {
		return err
	}
	publicKey, err := sshcopy.ReadPublicKey(keyPath)
	if err != nil {
		return err
	}
	password, err := guestPasswordInput(username, vmName)
	if err != nil {
		return err
	}
	if err := sshcopy.InstallPublicKey(resolution, username, password, publicKey); err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "Installed %s on %s@%s\n", keyPath, username, vmName)
	return err
}

func handlePort(w io.Writer, cfg config.Config, sess session.State, api *client.Client, accountHint string, args []string) error {
	if len(args) == 0 {
		return errors.New("port requires a subcommand")
	}
	switch args[0] {
	case "add":
		return handlePortAdd(w, cfg, sess, api, accountHint, args[1:])
	case "remove":
		return handlePortRemove(w, cfg, sess, api, accountHint, args[1:])
	case "list":
		return handlePortList(w, cfg, sess, api, accountHint, args[1:])
	default:
		return fmt.Errorf("unknown port subcommand %q", args[0])
	}
}

func handlePortAdd(w io.Writer, cfg config.Config, sess session.State, api *client.Client, accountHint string, args []string) error {
	account, err := resolveAccount(cfg, sess, api, accountHint)
	if err != nil {
		return err
	}
	vmName, remaining, err := resolveVMArgument(api, account.UUID, args)
	if err != nil {
		return err
	}
	if len(remaining) == 0 {
		return errors.New("port add requires a public:guest mapping")
	}
	publicPort, guestPort, err := parsePortMapping(remaining[0])
	if err != nil {
		return err
	}
	port, err := api.AddPublishedPort(account.UUID, vmName, publicPort, guestPort)
	if err != nil {
		return handleClientError(cfg.BaseURL, err)
	}
	_, err = fmt.Fprintf(w, "Published %d -> %d (%s)\n", port.PublicPort, port.GuestPort, port.Protocol)
	return err
}

func handlePortRemove(w io.Writer, cfg config.Config, sess session.State, api *client.Client, accountHint string, args []string) error {
	account, err := resolveAccount(cfg, sess, api, accountHint)
	if err != nil {
		return err
	}
	vmName, remaining, err := resolveVMArgument(api, account.UUID, args)
	if err != nil {
		return err
	}
	if len(remaining) == 0 {
		return errors.New("port remove requires a public port")
	}
	publicPort, err := strconv.Atoi(strings.TrimSpace(remaining[0]))
	if err != nil {
		return errors.New("public port must be numeric")
	}
	if err := api.RemovePublishedPort(account.UUID, vmName, publicPort); err != nil {
		return handleClientError(cfg.BaseURL, err)
	}
	_, err = fmt.Fprintf(w, "Removed published port %d from %s\n", publicPort, vmName)
	return err
}

func handlePortList(w io.Writer, cfg config.Config, sess session.State, api *client.Client, accountHint string, args []string) error {
	account, err := resolveAccount(cfg, sess, api, accountHint)
	if err != nil {
		return err
	}
	vmName, _, err := resolveVMArgument(api, account.UUID, args)
	if err != nil {
		return err
	}
	ports, err := api.ListPublishedPorts(account.UUID, vmName)
	if err != nil {
		return handleClientError(cfg.BaseURL, err)
	}
	if len(ports) == 0 {
		_, err := fmt.Fprintf(w, "No published ports for %s\n", vmName)
		return err
	}
	for _, port := range ports {
		if _, err := fmt.Fprintf(w, "%d\t%d\t%s\n", port.PublicPort, port.GuestPort, port.Protocol); err != nil {
			return err
		}
	}
	return nil
}

func finalizeAuthResult(w io.Writer, cfg *config.Config, api *client.Client, result client.AuthResult) error {
	if result.Challenge != nil {
		code, err := challengeInput(result.Challenge.Email, result.Challenge.Purpose)
		if err != nil {
			return err
		}
		verified, err := api.VerifyChallenge(result.Challenge.Email, result.Challenge.Purpose, code)
		if err != nil {
			return err
		}
		result = verified
	}
	if strings.TrimSpace(result.Token) == "" {
		return errors.New("authentication did not return a session token")
	}
	if err := secret.SaveToken(cfg.BaseURL, result.Token); err != nil {
		return err
	}
	cfg.LastEmail = result.User.Email
	meClient := client.New(cfg.BaseURL, result.Token)
	_, accounts, err := meClient.Me()
	if err == nil {
		if err := syncAccounts(cfg, accounts); err != nil {
			return err
		}
	}
	if err := config.Save(*cfg); err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "Authenticated as %s\n", result.User.Email)
	return err
}

func handleClientError(baseURL string, err error) error {
	var apiErr client.Error
	if errors.As(err, &apiErr) && apiErr.Code == "token_invalid" {
		_ = secret.DeleteToken(baseURL)
		return errors.New("authentication expired; stored token was deleted; please log in again")
	}
	return err
}

func syncAccounts(cfg *config.Config, accounts []client.Account) error {
	existingByUUID := make(map[string]config.AccountRef, len(cfg.Accounts))
	for _, ref := range cfg.Accounts {
		existingByUUID[ref.UUID] = ref
	}

	cfg.Accounts = map[string]config.AccountRef{}
	for _, account := range accounts {
		ref := config.AccountRef{
			UUID:    account.UUID,
			Name:    account.Name,
			BaseURL: cfg.BaseURL,
		}
		if existing, ok := existingByUUID[account.UUID]; ok {
			ref.Alias = existing.Alias
		}
		cfg.UpsertAccount(ref)
	}

	if cfg.DefaultAccount != "" {
		if _, _, ok := cfg.ResolveLocalAccount(cfg.DefaultAccount); !ok {
			cfg.DefaultAccount = ""
		}
	}
	if cfg.DefaultAccount == "" && len(cfg.Accounts) == 1 {
		for alias := range cfg.Accounts {
			cfg.DefaultAccount = alias
		}
	}
	return nil
}

func localAccountMarker(cfg config.Config, sess session.State, accountUUID string) string {
	alias, _, ok := cfg.ResolveLocalAccount(accountUUID)
	if !ok {
		return ""
	}
	markers := []string{" alias=" + alias}
	if cfg.DefaultAccount == alias {
		markers = append(markers, " default")
	}
	if sess.ActiveAccount == alias {
		markers = append(markers, " active")
	}
	return " [" + strings.Join(markers, ",") + "]"
}

func resolveAccount(cfg config.Config, sess session.State, api *client.Client, hint string) (client.Account, error) {
	accounts, err := api.ListAccounts()
	if err != nil {
		return client.Account{}, handleClientError(cfg.BaseURL, err)
	}
	if hint = strings.TrimSpace(hint); hint != "" {
		if alias, ref, ok := cfg.ResolveLocalAccount(hint); ok {
			for _, account := range accounts {
				if account.UUID == ref.UUID {
					if account.Role == "" {
						account.Role = alias
					}
					return account, nil
				}
			}
		}
		return resolveRemoteAccount(accounts, hint)
	}
	if sess.ActiveAccount != "" {
		if _, ref, ok := cfg.ResolveLocalAccount(sess.ActiveAccount); ok {
			for _, account := range accounts {
				if account.UUID == ref.UUID {
					return account, nil
				}
			}
		}
	}
	if cfg.DefaultAccount != "" {
		if _, ref, ok := cfg.ResolveLocalAccount(cfg.DefaultAccount); ok {
			for _, account := range accounts {
				if account.UUID == ref.UUID {
					return account, nil
				}
			}
		}
	}
	if len(accounts) == 1 {
		return accounts[0], nil
	}
	if tui.CanUse() {
		return selectRemoteAccount(accounts)
	}
	return client.Account{}, errors.New("multiple accounts available; use --account or set account default/use")
}

func resolveRemoteAccount(accounts []client.Account, identifier string) (client.Account, error) {
	identifier = strings.TrimSpace(identifier)
	for _, account := range accounts {
		if account.UUID == identifier || strings.EqualFold(account.Name, identifier) {
			return account, nil
		}
	}
	return client.Account{}, fmt.Errorf("account %q not found", identifier)
}

func resolveMember(members []client.AccountMember, identifier string) (string, error) {
	identifier = strings.TrimSpace(identifier)
	for _, member := range members {
		if member.UserUUID == identifier || strings.EqualFold(member.Email, identifier) {
			return member.UserUUID, nil
		}
	}
	return "", fmt.Errorf("member %q not found", identifier)
}

func selectRemoteAccount(accounts []client.Account) (client.Account, error) {
	if len(accounts) == 0 {
		return client.Account{}, errors.New("no accounts available")
	}
	if !tui.CanUse() {
		return client.Account{}, errors.New("multiple accounts available; use --account or set account default/use")
	}
	options := make([]tui.Option, 0, len(accounts))
	for _, account := range accounts {
		options = append(options, tui.Option{Label: account.Name})
	}
	index, err := tui.Select("Select an account", nil, options, 0)
	if err != nil {
		return client.Account{}, err
	}
	return accounts[index], nil
}

func selectLocalAccount(cfg config.Config) (config.AccountRef, error) {
	accounts := cfg.SortedAccounts()
	if len(accounts) == 0 {
		return config.AccountRef{}, errors.New("no local accounts configured")
	}
	if !tui.CanUse() {
		return config.AccountRef{}, errors.New("no account identifier provided")
	}
	options := make([]tui.Option, 0, len(accounts))
	for _, account := range accounts {
		options = append(options, tui.Option{Label: account.Alias})
	}
	index, err := tui.Select("Select a local account", nil, options, 0)
	if err != nil {
		return config.AccountRef{}, err
	}
	return accounts[index], nil
}

func loginInputs(lastEmail string) (string, string, error) {
	if tui.CanUse() {
		values, err := tui.Inputs("Login", nil, []tui.Field{
			{Key: "email", Label: "Email", Value: lastEmail},
			{Key: "password", Label: "Password", Secret: true},
		})
		if err != nil {
			return "", "", err
		}
		return values["email"], values["password"], nil
	}
	reader := bufio.NewReader(os.Stdin)
	email, err := prompt(reader, "Email", lastEmail)
	if err != nil {
		return "", "", err
	}
	password, err := promptPassword("Password")
	if err != nil {
		return "", "", err
	}
	return email, password, nil
}

func signupInputs(lastEmail string) (string, string, string, error) {
	if tui.CanUse() {
		values, err := tui.Inputs("Signup", nil, []tui.Field{
			{Key: "email", Label: "Email", Value: lastEmail},
			{Key: "password", Label: "Password", Secret: true},
			{Key: "account", Label: "Initial account name", Value: "My Account"},
		})
		if err != nil {
			return "", "", "", err
		}
		return values["email"], values["password"], values["account"], nil
	}
	reader := bufio.NewReader(os.Stdin)
	email, err := prompt(reader, "Email", lastEmail)
	if err != nil {
		return "", "", "", err
	}
	password, err := promptPassword("Password")
	if err != nil {
		return "", "", "", err
	}
	accountName, err := prompt(reader, "Initial account name", "My Account")
	if err != nil {
		return "", "", "", err
	}
	return email, password, accountName, nil
}

func forgotInputs(email string) (string, string, string, error) {
	if tui.CanUse() {
		values, err := tui.Inputs("Reset Password", []string{"Reset for " + email}, []tui.Field{
			{Key: "code", Label: "Email code"},
			{Key: "password", Label: "New password", Secret: true},
			{Key: "confirm", Label: "Confirm new password", Secret: true},
		})
		if err != nil {
			return "", "", "", err
		}
		return values["code"], values["password"], values["confirm"], nil
	}
	reader := bufio.NewReader(os.Stdin)
	code, err := prompt(reader, "Email code", "")
	if err != nil {
		return "", "", "", err
	}
	newPassword, err := promptPassword("New password")
	if err != nil {
		return "", "", "", err
	}
	confirm, err := promptPassword("Confirm new password")
	if err != nil {
		return "", "", "", err
	}
	return code, newPassword, confirm, nil
}

func transferFollowup() (bool, string, error) {
	if tui.CanUse() {
		choice, err := tui.Confirm("Transfer Complete", []string{"Create a new owned account now?"}, "Create account", "Skip")
		if err != nil {
			return false, "", err
		}
		if choice != "Create account" {
			return false, "", nil
		}
		values, err := tui.Inputs("New Account", nil, []tui.Field{{Key: "name", Label: "New account name", Value: "My Account"}})
		if err != nil {
			return false, "", err
		}
		return true, values["name"], nil
	}
	reader := bufio.NewReader(os.Stdin)
	answer, err := prompt(reader, "Create a new owned account? (y/N)", "N")
	if err != nil {
		return false, "", err
	}
	if strings.EqualFold(strings.TrimSpace(answer), "y") || strings.EqualFold(strings.TrimSpace(answer), "yes") {
		name, err := prompt(reader, "New account name", "My Account")
		if err != nil {
			return false, "", err
		}
		return true, name, nil
	}
	return false, "", nil
}

func challengeInput(email, purpose string) (string, error) {
	if tui.CanUse() {
		values, err := tui.Inputs("Email Challenge", []string{purpose + " for " + email}, []tui.Field{{Key: "code", Label: "Email code"}})
		if err != nil {
			return "", err
		}
		return values["code"], nil
	}
	reader := bufio.NewReader(os.Stdin)
	return prompt(reader, "Email code", "")
}

func guestPasswordInput(username, vmName string) (string, error) {
	if tui.CanUse() {
		values, err := tui.Inputs("Guest Password", []string{"Authenticate as " + username + " on " + vmName}, []tui.Field{{
			Key:    "password",
			Label:  "Password",
			Secret: true,
		}})
		if err != nil {
			return "", err
		}
		return values["password"], nil
	}
	return promptPassword("Guest password")
}

func selectPublicKeyPath() (string, error) {
	keys, err := sshcopy.DiscoverPublicKeys()
	if err != nil {
		return "", err
	}
	if tui.CanUse() {
		options := make([]tui.Option, 0, len(keys)+1)
		for _, key := range keys {
			options = append(options, tui.Option{Label: key.Label})
		}
		options = append(options, tui.Option{Label: "Enter custom path"})
		index, err := tui.Select("Select a public key", []string{"Choose which public key to install."}, options, 0)
		if err != nil {
			return "", err
		}
		if index < len(keys) {
			return keys[index].Path, nil
		}
		values, err := tui.Inputs("Custom Public Key Path", nil, []tui.Field{{Key: "path", Label: "Path"}})
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(values["path"]), nil
	}
	if len(keys) > 0 {
		return keys[0].Path, nil
	}
	reader := bufio.NewReader(os.Stdin)
	return prompt(reader, "Public key path", "")
}

func addInputs(vmName, guestPort string) (string, string, error) {
	if tui.CanUse() {
		values, err := tui.Inputs("Create VM", nil, []tui.Field{
			{Key: "name", Label: "VM name", Value: vmName},
			{Key: "port", Label: "Guest web port", Value: defaultString(guestPort, "80")},
		})
		if err != nil {
			return "", "", err
		}
		return values["name"], values["port"], nil
	}
	reader := bufio.NewReader(os.Stdin)
	if vmName == "" {
		value, err := prompt(reader, "VM name", "")
		if err != nil {
			return "", "", err
		}
		vmName = value
	}
	if guestPort == "" {
		value, err := prompt(reader, "Guest web port", "80")
		if err != nil {
			return "", "", err
		}
		guestPort = value
	}
	return vmName, guestPort, nil
}

func deleteConfirmation(vmName string) error {
	if tui.CanUse() {
		return tui.RequireText("Delete VM", []string{"Delete " + vmName, "Type DELETE to confirm."}, "Confirmation", "DELETE")
	}
	reader := bufio.NewReader(os.Stdin)
	confirmation, err := prompt(reader, "Type DELETE to confirm", "")
	if err != nil {
		return err
	}
	if confirmation != "DELETE" {
		return errors.New("delete cancelled")
	}
	return nil
}

func selectInviteAction(invites []client.Invite) (client.Invite, string, error) {
	options := make([]tui.Option, 0, len(invites))
	for _, invite := range invites {
		options = append(options, tui.Option{Label: invite.AccountName})
	}
	index, err := tui.Select("Pending Invites", []string{"Choose an invite to act on."}, options, 0)
	if err != nil {
		return client.Invite{}, "", err
	}
	action, err := tui.Confirm("Invite Action", []string{"Invite for " + invites[index].AccountName}, "accept", "refuse", "cancel")
	if err != nil {
		return client.Invite{}, "", err
	}
	return invites[index], action, nil
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func prompt(reader *bufio.Reader, label, defaultValue string) (string, error) {
	if defaultValue != "" {
		fmt.Fprintf(os.Stdout, "%s [%s]: ", label, defaultValue)
	} else {
		fmt.Fprintf(os.Stdout, "%s: ", label)
	}
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultValue, nil
	}
	return value, nil
}

func promptPassword(label string) (string, error) {
	fmt.Fprintf(os.Stdout, "%s: ", label)
	password, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stdout)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(password)), nil
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

func parsePortMapping(value string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 2 {
		return 0, 0, errors.New("port mapping must be in public:guest format")
	}
	publicPort, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, errors.New("public port must be numeric")
	}
	guestPort, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, errors.New("guest port must be numeric")
	}
	return publicPort, guestPort, nil
}

func resolveVMArgument(api *client.Client, accountUUID string, args []string) (string, []string, error) {
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		return strings.TrimSpace(args[0]), args[1:], nil
	}
	vms, err := api.ListVMs(accountUUID)
	if err != nil {
		return "", nil, err
	}
	if len(vms) == 0 {
		return "", nil, errors.New("no VMs are available")
	}
	selected, err := selectVM(vms)
	if err != nil {
		return "", nil, err
	}
	return selected, args, nil
}

func selectVM(vms []client.VM) (string, error) {
	if tui.CanUse() {
		options := make([]tui.Option, 0, len(vms))
		for _, vm := range vms {
			options = append(options, tui.Option{Label: vm.Name})
		}
		index, err := tui.Select("Select a VM", nil, options, 0)
		if err != nil {
			return "", err
		}
		return vms[index].Name, nil
	}
	reader := bufio.NewReader(os.Stdin)
	fmt.Fprintln(os.Stdout, "Select a VM:")
	for i, vm := range vms {
		fmt.Fprintf(os.Stdout, "  %d. %s\t%s\ttemplate=%s\tcpu=%d\tram=%dMB\n", i+1, vm.Name, vm.State, vm.TemplateName, vm.CPUCount, vm.MemoryMB)
	}
	selection, err := prompt(reader, "VM number", "")
	if err != nil {
		return "", err
	}
	index, err := strconv.Atoi(strings.TrimSpace(selection))
	if err != nil || index < 1 || index > len(vms) {
		return "", errors.New("invalid VM selection")
	}
	return vms[index-1].Name, nil
}

func firstArg(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func secondArg(values []string) string {
	if len(values) < 2 {
		return ""
	}
	return values[1]
}

func thirdArg(values []string) string {
	if len(values) < 3 {
		return ""
	}
	return values[2]
}

func hasFlag(values []string, target string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func printPlaceholder(w io.Writer, area string) error {
	_, err := fmt.Fprintf(w, "%s is not implemented yet\n", area)
	return err
}

func printable(value string) string {
	if value == "" {
		return "<none>"
	}
	return value
}

func currentAccountLabel(cfg config.Config, sess session.State) string {
	if sess.ActiveAccount != "" {
		if _, ref, ok := cfg.ResolveLocalAccount(sess.ActiveAccount); ok {
			return ref.Name
		}
		return sess.ActiveAccount
	}
	if cfg.DefaultAccount != "" {
		if _, ref, ok := cfg.ResolveLocalAccount(cfg.DefaultAccount); ok {
			return ref.Name
		}
		return cfg.DefaultAccount
	}
	return "<none>"
}

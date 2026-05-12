package app

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"rqstdev/cli/internal/client"
	"rqstdev/cli/internal/config"
	"rqstdev/cli/internal/secret"
	"rqstdev/cli/internal/session"
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
		return printRootHelp(w, cfg, sess)
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
		return printPlaceholder(w, "vm add")
	case "delete":
		return printPlaceholder(w, "vm delete")
	case "poweron":
		return handlePowerAction(w, cfg, sess, api, *accountHint, rest[1:], "poweron")
	case "poweroff":
		return handlePowerAction(w, cfg, sess, api, *accountHint, rest[1:], "poweroff")
	case "kill":
		return handlePowerAction(w, cfg, sess, api, *accountHint, rest[1:], "kill")
	case "ssh":
		return handleSSH(cfg, sess, api, *accountHint, rest[1:])
	case "port":
		return handlePort(w, rest[1:])
	case "help":
		return printRootHelp(w, cfg, sess)
	default:
		return fmt.Errorf("unknown command %q", rest[0])
	}
}

func printRootHelp(w io.Writer, cfg config.Config, sess session.State) error {
	_, err := fmt.Fprintf(
		w,
		"rqstdev CLI\n\nServer: %s\nDefault account: %s\nSession account: %s\n\nCommands:\n  signup\n  login\n  logout\n  account\n  invites\n  list\n  add\n  delete\n  poweron\n  poweroff\n  kill\n  ssh\n  port\n",
		cfg.BaseURL,
		printable(cfg.DefaultAccount),
		printable(sess.ActiveAccount),
	)
	return err
}

func handleUnauthenticatedRoot(w io.Writer, cfg *config.Config) error {
	reader := bufio.NewReader(os.Stdin)
	choice, err := prompt(reader, "Not authenticated. Choose login or signup", "login")
	if err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "", "login":
		return handleLogin(w, cfg)
	case "signup":
		return handleSignup(w, cfg)
	default:
		return fmt.Errorf("unknown choice %q", choice)
	}
}

func handleSignup(w io.Writer, cfg *config.Config) error {
	reader := bufio.NewReader(os.Stdin)
	email, err := prompt(reader, "Email", cfg.LastEmail)
	if err != nil {
		return err
	}
	password, err := promptPassword("Password")
	if err != nil {
		return err
	}
	accountName, err := prompt(reader, "Initial account name", "My Account")
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
	reader := bufio.NewReader(os.Stdin)
	email, err := prompt(reader, "Email", cfg.LastEmail)
	if err != nil {
		return err
	}
	password, err := promptPassword("Password")
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
			return errors.New("account create requires a name")
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
	if len(args) == 0 {
		return errors.New("account add requires a visible remote account name or UUID")
	}
	accounts, err := api.ListAccounts()
	if err != nil {
		return handleClientError(cfg.BaseURL, err)
	}
	target, err := resolveRemoteAccount(accounts, args[0])
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
	reader := bufio.NewReader(os.Stdin)
	code, err := prompt(reader, "Email code", "")
	if err != nil {
		return err
	}
	newPassword, err := promptPassword("New password")
	if err != nil {
		return err
	}
	confirm, err := promptPassword("Confirm new password")
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

	reader := bufio.NewReader(os.Stdin)
	answer, err := prompt(reader, "Create a new owned account? (y/N)", "N")
	if err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(answer), "y") || strings.EqualFold(strings.TrimSpace(answer), "yes") {
		name, err := prompt(reader, "New account name", "My Account")
		if err != nil {
			return err
		}
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
		for _, invite := range invites {
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", invite.UUID, invite.AccountName, invite.Role, invite.CreatedAt); err != nil {
				return err
			}
		}
		return nil
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

func handlePowerAction(w io.Writer, cfg config.Config, sess session.State, api *client.Client, accountHint string, args []string, action string) error {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return fmt.Errorf("%s requires a VM name", action)
	}
	account, err := resolveAccount(cfg, sess, api, accountHint)
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
		return handleClientError(cfg.BaseURL, err)
	}
	_, err = fmt.Fprintf(w, "%s\t%s\tssh=%t\tweb=%t\n", vm.Name, vm.State, vm.SSHReady, vm.WebReady)
	return err
}

func handleSSH(cfg config.Config, sess session.State, api *client.Client, accountHint string, args []string) error {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return errors.New("ssh requires a VM name")
	}
	account, err := resolveAccount(cfg, sess, api, accountHint)
	if err != nil {
		return err
	}
	username, vmName := parseSSHVMTarget(args[0])
	if vmName == "" {
		return errors.New("ssh requires a VM name")
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

func handlePort(w io.Writer, args []string) error {
	if len(args) == 0 {
		return errors.New("port requires a subcommand")
	}
	switch args[0] {
	case "add", "remove", "list":
		return printPlaceholder(w, "port "+args[0])
	default:
		return fmt.Errorf("unknown port subcommand %q", args[0])
	}
}

func finalizeAuthResult(w io.Writer, cfg *config.Config, api *client.Client, result client.AuthResult) error {
	if result.Challenge != nil {
		reader := bufio.NewReader(os.Stdin)
		code, err := prompt(reader, "Email code", "")
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
	for _, account := range accounts {
		cfg.UpsertAccount(config.AccountRef{
			UUID:    account.UUID,
			Name:    account.Name,
			BaseURL: cfg.BaseURL,
		})
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
			return client.Account{UUID: ref.UUID, Name: ref.Name, Role: alias}, nil
		}
		return resolveRemoteAccount(accounts, hint)
	}
	if sess.ActiveAccount != "" {
		if _, ref, ok := cfg.ResolveLocalAccount(sess.ActiveAccount); ok {
			return client.Account{UUID: ref.UUID, Name: ref.Name}, nil
		}
	}
	if cfg.DefaultAccount != "" {
		if _, ref, ok := cfg.ResolveLocalAccount(cfg.DefaultAccount); ok {
			return client.Account{UUID: ref.UUID, Name: ref.Name}, nil
		}
	}
	if len(accounts) == 1 {
		return accounts[0], nil
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

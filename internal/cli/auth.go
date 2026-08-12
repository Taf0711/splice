package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/oauth"
	"github.com/Taf0711/splice/internal/provideroauth"
	"github.com/Taf0711/splice/internal/redaction"
)

// runAuth dispatches `splice auth <command>` for provider OAuth login. It is
// additive and independent of `splice mcp oauth` (MCP server auth), which is
// unchanged.
func runAuth(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	if len(args) == 0 {
		return writeExecUsageError(stderr, "auth subcommand required. Use `splice auth status`.")
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		if err := writeAuthHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if deps.migrateOAuth != nil {
		deps.migrateOAuth()
	}
	switch args[0] {
	case "login":
		return runAuthLogin(args[1:], stdout, stderr, deps)
	case "logout":
		return runAuthLogout(args[1:], stdout, stderr, deps)
	case "status":
		return runAuthStatus(args[1:], stdout, stderr, deps)
	case "refresh":
		return runAuthRefresh(args[1:], stdout, stderr, deps)
	case "openrouter":
		return runAuthOpenRouter(args[1:], stdout, stderr, deps)
	case "chatgpt":
		return runAuthChatGPT(args[1:], stdout, stderr, deps)
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown auth subcommand %q", args[0]))
	}
}

// runAuthOpenRouter runs OpenRouter's browser PKCE login and prints the freshly
// minted API key. Unlike `auth login` (which stores an OAuth bearer token),
// OpenRouter's flow mints a normal API key; the setup wizard saves it to a
// provider profile, while this command prints it for manual configuration.
func runAuthOpenRouter(args []string, stdout io.Writer, stderr io.Writer, _ appDeps) int {
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "help" {
			_ = writeAuthHelp(stdout)
			return exitSuccess
		}
	}
	// openrouter takes no positional args or flags; reject the unexpected so a
	// typo/unsupported flag fails fast instead of silently running the login.
	if len(args) > 0 {
		return writeExecUsageError(stderr, fmt.Sprintf("splice auth openrouter takes no arguments (got %q)", args[0]))
	}
	key, err := provideroauth.OpenRouterLogin(context.Background(), provideroauth.OpenRouterOptions{
		Out:        stdout,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		// SPLICE_OPENROUTER_BASE_URL overrides the endpoint (self-hosted gateway or tests).
		BaseURL: strings.TrimSpace(os.Getenv("SPLICE_OPENROUTER_BASE_URL")),
	})
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if _, err := fmt.Fprintf(stdout, "\nOpenRouter login complete — new API key minted.\nUse it with splice, e.g.:\n  export OPENROUTER_API_KEY=%s\n(or add it to a provider profile with catalogID \"openrouter\").\n", key); err != nil {
		return exitCrash
	}
	return exitSuccess
}

// runAuthChatGPT runs the ChatGPT (Codex) browser PKCE login, persists the
// bearer + chatgpt-account-id claim under the "chatgpt" provider key, and
// prints a status block. The bearer routes to chatgpt.com/backend-api/codex
// for ChatGPT Plus/Pro/Business/Enterprise subscribers; a successful login
// makes the agent use the chatgpt catalog entry with the OAuth bearer.
func runAuthChatGPT(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	storage, err := resolveCLIAuthStorage(deps, "")
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	return runAuthChatGPTWithStorage(args, stdout, stderr, deps, storage, "")
}

func runAuthChatGPTWithStorage(args []string, stdout io.Writer, stderr io.Writer, deps appDeps, storage, persistStorage string) int {
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "help" {
			_ = writeAuthHelp(stdout)
			return exitSuccess
		}
	}
	if len(args) > 0 {
		return writeExecUsageError(stderr, fmt.Sprintf("splice auth chatgpt takes no arguments (got %q)", args[0]))
	}

	// Build the same env map the oauth engine reads so the chatgpt preset is
	// opted into (the preset is off by default to keep third-party OAuth
	// client identities out of the default credential path). The env is
	// layered: process env first, then SPLICE_OAUTH_ALLOW_PRESETS=1.
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			env[kv[:eq]] = kv[eq+1:]
		}
	}
	env["SPLICE_OAUTH_ALLOW_PRESETS"] = "1"

	token, err := provideroauth.ChatGPTLogin(context.Background(), provideroauth.ChatGPTOptions{
		Env:        env,
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
		Out:        stdout,
		// Don't auto-open a browser — print the URL to stdout and let the
		// user click it. (Same posture as runAuthOpenRouter; the headless
		// sandbox context makes launching a browser a worse default than
		// printing the URL.)
		OpenBrowser: func(string) error { return nil },
	})
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}

	// Persist via the oauth manager's store so the same path
	// splice auth status / splice auth refresh / TokenResolver use is hit.
	// We bypass Manager.Login because the account-id extraction happens
	// inside provideroauth.ChatGPTLogin; the manager would not pick up
	// the customized Token.Account field.
	store, err := oauth.NewStore(oauth.StoreOptions{Now: deps.now, Storage: storage})
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(fmt.Errorf("open OAuth %s backend: %w", displayAuthBackend(storage), err), redaction.Options{}), exitCrash)
	}
	if err := store.Save(oauth.ProviderKey("chatgpt"), token); err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if err := persistCLIAuthStorage(deps, persistStorage); err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	statuses, err := store.Status("provider:chatgpt")
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	formatted := oauth.FormatStatuses(statuses)
	if len(statuses) == 0 {
		formatted = "ChatGPT login complete."
		if strings.TrimSpace(token.Account) != "" {
			formatted += "\nChatGPT account id: " + token.Account
		}
	}
	if _, err := fmt.Fprint(stdout, formatted); err != nil {
		return exitCrash
	}
	if _, err := fmt.Fprint(stdout, "\nUse it with splice, e.g.:\n  splice --provider chatgpt --model gpt-5.5\n"); err != nil {
		return exitCrash
	}
	return exitSuccess
}

// authArgs is the parsed form of an auth subcommand's arguments.
type authArgs struct {
	positional []string
	json       bool
	device     bool
	watch      bool
	scopes     []string
	storage    string
	help       bool
}

func parseAuthArgs(sub string, args []string) (authArgs, error) {
	var parsed authArgs
	addScope := func(scope string) error {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			return fmt.Errorf("--scope requires a non-empty value")
		}
		parsed.scopes = append(parsed.scopes, scope)
		return nil
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			parsed.help = true
		case arg == "--json":
			parsed.json = true
		case arg == "--device":
			parsed.device = true
		case arg == "--watch":
			parsed.watch = true
		case arg == "--scope":
			if i+1 >= len(args) {
				return authArgs{}, fmt.Errorf("--scope requires a value")
			}
			i++
			if err := addScope(args[i]); err != nil {
				return authArgs{}, err
			}
		case strings.HasPrefix(arg, "--scope="):
			if err := addScope(strings.TrimPrefix(arg, "--scope=")); err != nil {
				return authArgs{}, err
			}
		case arg == "--storage":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return authArgs{}, fmt.Errorf("--storage requires a value")
			}
			i++
			parsed.storage = args[i]
		case strings.HasPrefix(arg, "--storage="):
			parsed.storage = strings.TrimPrefix(arg, "--storage=")
			if strings.TrimSpace(parsed.storage) == "" {
				return authArgs{}, fmt.Errorf("--storage requires a value")
			}
		case strings.HasPrefix(arg, "-"):
			return authArgs{}, fmt.Errorf("unknown flag %q", arg)
		default:
			parsed.positional = append(parsed.positional, arg)
		}
	}
	if parsed.help {
		return parsed, nil // help short-circuits flag validation
	}
	if err := validateAuthFlags(sub, parsed); err != nil {
		return authArgs{}, err
	}
	return parsed, nil
}

// validateAuthFlags rejects flags a subcommand does not accept, so an ambiguous
// invocation fails fast instead of silently ignoring a flag.
func validateAuthFlags(sub string, a authArgs) error {
	allowed := map[string]map[string]bool{
		"login":   {"device": true, "scope": true, "storage": true},
		"logout":  {"json": true},
		"status":  {"json": true},
		"refresh": {"watch": true},
	}[sub]
	bad := func(name string) error { return fmt.Errorf("splice auth %s does not accept %s", sub, name) }
	if a.json && !allowed["json"] {
		return bad("--json")
	}
	if a.device && !allowed["device"] {
		return bad("--device")
	}
	if a.watch && !allowed["watch"] {
		return bad("--watch")
	}
	if len(a.scopes) > 0 && !allowed["scope"] {
		return bad("--scope")
	}
	if strings.TrimSpace(a.storage) != "" && !allowed["storage"] {
		return bad("--storage")
	}
	if strings.TrimSpace(a.storage) != "" {
		if _, err := config.ValidateAuthStorage(a.storage); err != nil {
			return err
		}
	}
	return nil
}

// newAuthManager builds an OAuth manager and reports its active backend. User
// auth.storage has priority over SPLICE_OAUTH_STORAGE. An empty selector keeps
// automatic backend selection.
func newAuthManager(deps appDeps, out io.Writer) (*oauth.Manager, string, error) {
	storage, err := resolveCLIAuthStorage(deps, "")
	if err != nil {
		return nil, "", err
	}
	return newAuthManagerWithStorage(deps, out, storage)
}

func newAuthManagerWithStorage(deps appDeps, out io.Writer, storage string) (*oauth.Manager, string, error) {
	store, err := oauth.NewStore(oauth.StoreOptions{Now: deps.now, Storage: storage})
	if err != nil {
		return nil, "", fmt.Errorf("open OAuth %s backend: %w", displayAuthBackend(storage), err)
	}
	manager, err := oauth.NewManager(oauth.ManagerOptions{
		Store:      store,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Now:        deps.now,
		Out:        out,
		// The opener prints the URL so headless shells can copy it; the URL
		// carries no token material. A real browser launch is intentionally not
		// performed (the sandbox/headless contexts make printing the safer default).
		OpenBrowser: func(string) error { return nil },
		// `splice auth login <preset>` (e.g. xai) should resolve the baked-in preset
		// without the operator exporting SPLICE_OAUTH_ALLOW_PRESETS first.
		AllowPresets: true,
	})
	if err != nil {
		return nil, "", fmt.Errorf("create OAuth manager: %w", err)
	}
	return manager, store.Backend(), nil
}

func resolveCLIAuthStorage(deps appDeps, selected string) (string, error) {
	if strings.TrimSpace(selected) != "" {
		return config.ValidateAuthStorage(selected)
	}
	if deps.userConfigPath == nil {
		return config.ResolveAuthStorageAt("", config.OAuthStorageEnv)
	}
	path, err := deps.userConfigPath()
	if err != nil {
		return "", fmt.Errorf("resolve user config path: %w", err)
	}
	return config.ResolveAuthStorageAt(path, config.OAuthStorageEnv)
}

func persistCLIAuthStorage(deps appDeps, storage string) error {
	if strings.TrimSpace(storage) == "" {
		return nil
	}
	if deps.userConfigPath == nil {
		return fmt.Errorf("resolve user config path: unavailable")
	}
	path, err := deps.userConfigPath()
	if err != nil {
		return fmt.Errorf("resolve user config path: %w", err)
	}
	if _, err := config.SetAuthStorage(path, storage); err != nil {
		return fmt.Errorf("save auth.storage: %w", err)
	}
	return nil
}

func runAuthLogin(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	parsed, err := parseAuthArgs("login", args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if parsed.help {
		_ = writeAuthHelp(stdout)
		return exitSuccess
	}
	if len(parsed.positional) != 1 {
		return writeExecUsageError(stderr, "usage: splice auth login <provider> [--device] [--scope <scope>] [--storage <keyring|encrypted-file|file>]")
	}
	provider := parsed.positional[0]
	storage, err := resolveCLIAuthStorage(deps, parsed.storage)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	// ChatGPT (Codex) requires a fixed redirect_uri (http://localhost:1455/
	// auth/callback) and mandatory authorize params (id_token_add_organizations,
	// codex_cli_simplified_flow, originator) that the generic loopback flow
	// cannot supply. Route it to the dedicated ChatGPT login so
	// `splice auth login chatgpt` behaves identically to `splice auth chatgpt`.
	if strings.EqualFold(provider, "chatgpt") {
		if parsed.device {
			return writeExecUsageError(stderr, "ChatGPT login does not support --device (it is loopback-only)")
		}
		if len(parsed.scopes) > 0 {
			return writeExecUsageError(stderr, "ChatGPT login does not support --scope (the required scopes are fixed by the Codex client registration)")
		}
		return runAuthChatGPTWithStorage(nil, stdout, stderr, deps, storage, parsed.storage)
	}
	manager, _, err := newAuthManagerWithStorage(deps, stdout, storage)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	status, err := manager.Login(context.Background(), oauth.LoginOptions{
		Provider:    provider,
		Device:      parsed.device,
		ExtraScopes: parsed.scopes,
	})
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if err := persistCLIAuthStorage(deps, parsed.storage); err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if _, err := fmt.Fprintf(stdout, "Logged in to %s.\n%s\n", parsed.positional[0], oauth.FormatStatuses([]oauth.Status{status})); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runAuthLogout(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	parsed, err := parseAuthArgs("logout", args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if parsed.help {
		_ = writeAuthHelp(stdout)
		return exitSuccess
	}
	if len(parsed.positional) != 1 {
		return writeExecUsageError(stderr, "usage: splice auth logout <provider>")
	}
	provider := parsed.positional[0]
	manager, _, err := newAuthManager(deps, stdout)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	removed, err := manager.Logout(provider)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	// Also drop any stored API key and its marker so `auth logout` clears the whole
	// credential (OAuth token AND key), not just the OAuth side. Surface deletion
	// failures rather than reporting success while a credential remains.
	keyRemoved, keyErr := config.ForgetProviderKey(provider)
	if keyErr != nil {
		return writeAppError(stderr, redaction.ErrorMessage(keyErr, redaction.Options{}), exitCrash)
	}
	if configPath, perr := deps.userConfigPath(); perr == nil {
		if _, clearErr := config.ClearProviderKeyStored(configPath, provider); clearErr != nil {
			return writeAppError(stderr, redaction.ErrorMessage(clearErr, redaction.Options{}), exitCrash)
		}
	}
	removed = removed || keyRemoved
	if parsed.json {
		payload := struct {
			Provider string `json:"provider"`
			Removed  bool   `json:"removed"`
		}{Provider: provider, Removed: removed}
		if err := writePrettyJSON(stdout, payload); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	msg := fmt.Sprintf("No stored credential for %s.\n", provider)
	if removed {
		msg = fmt.Sprintf("Logged out of %s.\n", provider)
	}
	if _, err := fmt.Fprint(stdout, msg); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runAuthStatus(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	parsed, err := parseAuthArgs("status", args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if parsed.help {
		_ = writeAuthHelp(stdout)
		return exitSuccess
	}
	if len(parsed.positional) > 1 {
		return writeExecUsageError(stderr, "usage: splice auth status [provider]")
	}
	manager, backend, err := newAuthManager(deps, stdout)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	statuses, err := manager.StatusAll()
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if len(parsed.positional) == 1 {
		statuses = filterAuthStatuses(statuses, parsed.positional[0])
	}
	if parsed.json {
		payload := struct {
			Storage string         `json:"storage"`
			Logins  []oauth.Status `json:"logins"`
		}{Storage: backend, Logins: statuses}
		if err := writePrettyJSON(stdout, payload); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintf(stdout, "Storage: %s\n%s\n", backend, oauth.FormatStatuses(statuses)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runAuthRefresh(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	parsed, err := parseAuthArgs("refresh", args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if parsed.help {
		_ = writeAuthHelp(stdout)
		return exitSuccess
	}
	if len(parsed.positional) != 1 {
		return writeExecUsageError(stderr, "usage: splice auth refresh <provider>")
	}
	provider := parsed.positional[0]
	manager, _, err := newAuthManager(deps, stdout)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	key := oauth.ProviderKey(provider)
	if parsed.watch {
		return runAuthRefreshWatch(manager, key, provider, stdout, stderr)
	}
	if _, err := manager.Handle401(context.Background(), key); err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if _, err := fmt.Fprintf(stdout, "Refreshed OAuth token for %s.\n", provider); err != nil {
		return exitCrash
	}
	return exitSuccess
}

// runAuthRefreshWatch keeps a provider's token fresh in the foreground until
// interrupted. This is the opt-in proactive-refresh scheduler surface (for a
// long-running external process that reads the token file). It validates a
// refreshable token exists first, then schedules refreshes before each expiry.
func runAuthRefreshWatch(manager *oauth.Manager, key, provider string, stdout io.Writer, stderr io.Writer) int {
	ctx, stop := signalContext()
	defer stop()
	// Validate (and refresh-if-needed) once up front so a missing/non-refreshable
	// token fails fast instead of silently watching nothing.
	if _, err := manager.GetFresh(ctx, key); err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	scheduler := oauth.NewRefreshScheduler()
	scheduler.Start(ctx, manager, key)
	defer scheduler.Stop()
	if _, err := fmt.Fprintf(stdout, "Watching %s — refreshing before expiry. Press Ctrl+C to stop.\n", provider); err != nil {
		return exitCrash
	}
	<-ctx.Done()
	return exitSuccess
}

func filterAuthStatuses(statuses []oauth.Status, provider string) []oauth.Status {
	want := oauth.ProviderKey(provider)
	filtered := make([]oauth.Status, 0, 1)
	for _, st := range statuses {
		if st.Key == want {
			filtered = append(filtered, st)
		}
	}
	return filtered
}

func writeAuthHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  splice auth <command>

Commands:
  login <provider> [--device] [--scope <scope>] [--storage <backend>]
                                                    Log in to a provider via OAuth
  logout <provider>                               Delete a provider's stored login
  status [provider]                               Show login presence/expiry (never the token)
  refresh <provider> [--watch]                    Force a token refresh (--watch keeps it fresh)
  openrouter                                      Log in to OpenRouter in the browser; mints an API key
  chatgpt                                         Log in to ChatGPT in the browser (Codex backend, ChatGPT Plus/Pro)

A provider is any OAuth 2.0 / OIDC server. "openrouter" ('splice auth openrouter')
works out of the box. "xai" ('splice auth login xai') uses a built-in preset that is
off by default — enable it with SPLICE_OAUTH_ALLOW_PRESETS=1, or set the
SPLICE_OAUTH_XAI_* vars yourself. "chatgpt" ('splice auth login chatgpt' or
'splice auth chatgpt') uses a fixed-port loopback flow against the Codex backend.
Any preset field is overridable via the env vars below. For a custom provider named <name>, set:
  SPLICE_OAUTH_<NAME>_CLIENT_ID       (required)
  SPLICE_OAUTH_<NAME>_CLIENT_SECRET   (optional)
  SPLICE_OAUTH_<NAME>_AUTHORIZE_URL   SPLICE_OAUTH_<NAME>_TOKEN_URL
  SPLICE_OAUTH_<NAME>_DEVICE_URL      SPLICE_OAUTH_<NAME>_ISSUER_URL (for discovery)
  SPLICE_OAUTH_<NAME>_SCOPES          SPLICE_OAUTH_<NAME>_FLOW (loopback|device)
Endpoint URLs must be https (loopback exempt).

Storage: macOS uses the OS keyring by default. Other platforms use an encrypted
file under $XDG_CONFIG_HOME/splice. Use --storage on login to save auth.storage.
SPLICE_OAUTH_STORAGE selects OAuth storage when auth.storage is not set.
SPLICE_CRED_STORAGE does the same for API keys. The backends are keyring,
encrypted-file, and file. The file backend is plaintext with 0600 permissions.
MCP server tokens share the OAuth store.

Flags:
      --device   Use the device-code flow (headless/SSH; no browser)
      --scope    Add an OAuth scope (repeatable)
      --storage  Select keyring, encrypted-file, or file (login only)
      --watch    Keep the token fresh in the foreground (refresh only)
      --json     Print result as JSON (status/logout)
  -h, --help     Show this help
`)
	return err
}

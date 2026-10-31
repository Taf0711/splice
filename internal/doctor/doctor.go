package doctor

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/flags"
	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/projectfile"
	"github.com/Taf0711/splice/internal/providerhealth"
	"github.com/Taf0711/splice/internal/providers/decisions"
	"github.com/Taf0711/splice/internal/redaction"
	"github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

type Status string

const (
	StatusPass Status = "pass"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

type Check struct {
	ID      string         `json:"id"`
	Label   string         `json:"label"`
	Status  Status         `json:"status"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type Report struct {
	GeneratedAt string  `json:"generatedAt"`
	OK          bool    `json:"ok"`
	Checks      []Check `json:"checks"`
}

// Options injects a credential-store opener so the stored-key check is
// deterministic in tests. Nil means config.ProviderKeyStoreAt on UserConfig.
type Options struct {
	Now           func() time.Time
	Runtime       string
	UserConfig    string
	ProjectConfig string
	Provider      config.ProviderProfile
	// DecisionAdvisor is the resolved decision_advisor section. It tells the
	// trajectory classifier line which profile and model run the classifier.
	DecisionAdvisor config.DecisionAdvisorConfig
	// Evaluator is the decision evaluator the run would use, built by the same
	// call the run makes (decisions.NewEvaluator). Nil means no evaluator, so no
	// advisor starts. Doctor sends no request through it.
	Evaluator zeroruntime.DecisionEvaluator
	// Getenv reads the advisor environment variables. Nil means os.Getenv.
	Getenv func(string) string
	// TrajectoryProfile is the profile the default trajectory classifier runs
	// on. The line reports it as on only when its sign-in credential loads.
	TrajectoryProfile config.ProviderProfile
	// TrajectoryCredentialLoadable reports whether a profile's sign-in
	// credential is present and readable. Nil means the real credential store.
	// Tests inject a stub.
	TrajectoryCredentialLoadable func(config.ProviderProfile) bool
	WorkspaceRoot                string
	Sandbox                      config.SandboxConfig
	Connectivity                 bool
	ProviderHealth               *providerhealth.Result
	// OpenKeyStore reads the credential store for the stored-key reachability
	// check. Nil means config.ProviderKeyStoreAt(dir(UserConfig)). Tests inject
	// a stub so the check never touches a real keychain.
	OpenKeyStore func(userConfigDir string) (config.APIKeyGetter, string, error)
	// GOOS overrides the platform used to resolve the sandbox backend. Empty
	// means runtime.GOOS. Tests set it to assert platform-specific remedies.
	GOOS string
	// LookupExecutable resolves a binary on PATH for the sandbox-backend and
	// LSP-server checks. Nil means exec.LookPath; tests inject a stub so the
	// checks are deterministic regardless of the host's installed tooling.
	LookupExecutable func(string) (string, error)
	// Flags is the resolved flag set. Experimental flags that are on are reported.
	Flags flags.Set
	// ConfigError is a config resolve failure that is not a missing provider,
	// such as an invalid flag value. It becomes a failed config.resolve check.
	// Provider config is then reported as not checked, never as absent.
	ConfigError error
}

func Run(options Options) Report {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	checks := []Check{
		runtimeCheck(options.Runtime),
		configFilesCheck(options.UserConfig, options.ProjectConfig),
		configValidationCheck(options.UserConfig, options.ProjectConfig),
	}
	if options.ConfigError != nil {
		checks = append(checks, check("config.resolve", "Config resolution", StatusFail, options.ConfigError.Error(), nil))
	}
	var providerCheck Check
	if options.ConfigError != nil {
		providerCheck = check("provider.config", "Provider config", StatusWarn, "Provider config was not checked because config did not resolve. See config.resolve.", nil)
	} else {
		providerCheck = providerConfigCheck(hydrateStoredKey(options.Provider, options.UserConfig, options.OpenKeyStore))
	}
	checks = append(checks, providerCheck)
	modelCheck := providerModelCheck(options.Provider)
	checks = append(checks, modelCheck)
	checks = append(checks, connectivityCheck(options.Provider, options.Connectivity, modelCheck.Status, options.ProviderHealth))
	checks = append(checks, sandboxBackendCheck(options.GOOS, options.LookupExecutable, options.WorkspaceRoot, options.Sandbox))
	checks = append(checks, lspServersCheck(options.LookupExecutable))
	checks = append(checks, storedKeyReachabilityCheck(options.Provider, options.UserConfig, options.OpenKeyStore))
	if check, ok := experimentalFlagsCheck(options.Flags); ok {
		checks = append(checks, check)
	}
	trajectoryLoadable := true
	if options.DecisionAdvisor.TrajectoryDefault {
		loadable := options.TrajectoryCredentialLoadable
		if loadable == nil {
			loadable = chatGPTCredentialLoadable
		}
		trajectoryLoadable = loadable(options.TrajectoryProfile)
	}
	getenv := options.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	checks = append(checks, trajectoryClassifierCheck(options.Flags, options.DecisionAdvisor, options.Evaluator, getenv, trajectoryLoadable))

	report := Report{
		GeneratedAt: now().UTC().Format(time.RFC3339),
		OK:          true,
		Checks:      checks,
	}
	for _, check := range checks {
		if check.Status == StatusFail {
			report.OK = false
			break
		}
	}
	return report
}

func (report Report) Check(id string) *Check {
	for index := range report.Checks {
		if report.Checks[index].ID == id {
			return &report.Checks[index]
		}
	}
	return nil
}

func Format(report Report) string {
	lines := []string{
		fmt.Sprintf("Splice doctor report (%s)", redaction.RedactString(report.GeneratedAt, redaction.Options{})),
		fmt.Sprintf("Overall: %s", passFail(report.OK)),
	}
	for _, check := range report.Checks {
		lines = append(lines, fmt.Sprintf("[%s] %s - %s", check.Status, redaction.RedactString(check.ID, redaction.Options{}), redaction.RedactString(check.Message, redaction.Options{})))
		if details := formatDetails(check.Details); details != "" {
			lines = append(lines, "  "+details)
		}
	}
	return strings.Join(lines, "\n")
}

// experimentalFlagsCheck warns when an experimental flag is on. It reports
// nothing when no experimental flag is on.
func experimentalFlagsCheck(set flags.Set) (Check, bool) {
	var on []string
	for _, def := range flags.Registry {
		if def.Stability == flags.Experimental && set.Enabled(def.Name) {
			on = append(on, string(def.Name))
		}
	}
	if len(on) == 0 {
		return Check{}, false
	}
	return check("flags.experimental", "Experimental flags", StatusWarn, "experimental flags on: "+strings.Join(on, ", "), map[string]any{"flags": on}), true
}

// chatGPTCredentialLoadable reports whether a profile can authenticate the
// default classifier: its own configured key, or a stored sign-in login that
// the store reads. It is the same lookup the decision provider runs, so the
// line never says "on" for a run that would fail at the first request.
func chatGPTCredentialLoadable(profile config.ProviderProfile) bool {
	return decisions.ClassifierCredentialLoadable(profile)
}

// trajectoryClassifierCheck reports what the trajectory classifier does in this
// run. It asks the runtime predicate (splice.RealizeDecisionAdvisor), so the
// line cannot drift from what a run builds. credentialOK is false when the
// default classifier's profile has no usable sign-in credential. It is an
// informational pass: it never fails.
func trajectoryClassifierCheck(set flags.Set, section config.DecisionAdvisorConfig, evaluator zeroruntime.DecisionEvaluator, getenv func(string) string, credentialOK bool) Check {
	const id, label = "trajectory.classifier", "Trajectory classifier"
	if !set.Enabled(flags.TrajectoryClassifierEnabled) {
		return check(id, label, StatusPass, "trajectory classifier: off (pipeline.trajectory_classifier is off)", nil)
	}
	// A dormant configured section is replaced by the default. The note says so.
	note := ""
	if section.ExplicitInactive {
		note = "; the decision_advisor section is inactive until pipeline.decision_advisor is on"
	}
	if section.Mode == "off" && !section.TrajectoryDefault {
		return check(id, label, StatusPass, "trajectory classifier: off (decision_advisor mode is off)", nil)
	}
	if section.TrajectoryDefault && !credentialOK {
		return check(id, label, StatusPass, "trajectory classifier: rules only (ChatGPT sign-in has no usable credential)"+note, nil)
	}
	if !section.TrajectoryDefault && section.Mode != "" && !set.Enabled(flags.DecisionAdvisorEnabled) {
		return check(id, label, StatusPass, "trajectory classifier: rules only (the decision_advisor section is inactive until pipeline.decision_advisor is on)", nil)
	}
	realized, err := splice.RealizeDecisionAdvisor(set, evaluator, section, getenv)
	if err != nil {
		return check(id, label, StatusWarn, fmt.Sprintf("trajectory classifier: not active, configuration error: %v", err), nil)
	}
	switch {
	case section.TrajectoryDefault && realized.Active && realized.Classifier:
		return check(id, label, StatusPass, fmt.Sprintf("trajectory classifier: on via ChatGPT sign-in profile %s (model %s)", section.ProviderProfile, realized.Model)+note, map[string]any{"profile": section.ProviderProfile, "model": realized.Model})
	case section.TrajectoryDefault:
		return check(id, label, StatusPass, "trajectory classifier: rules only (the ChatGPT sign-in profile could not start)"+note, nil)
	case realized.Active:
		return check(id, label, StatusPass, fmt.Sprintf("trajectory classifier: configured in decision_advisor (mode %s)", realized.Mode), nil)
	case section.Mode != "":
		return check(id, label, StatusPass, "trajectory classifier: rules only (decision_advisor is off)", nil)
	default:
		return check(id, label, StatusPass, "trajectory classifier: rules only (no ChatGPT sign-in)", nil)
	}
}

func runtimeCheck(runtime string) Check {
	runtime = strings.TrimSpace(runtime)
	if runtime == "" {
		runtime = "go"
	}
	return check("runtime.go", "Go runtime", StatusPass, fmt.Sprintf("Splice Go runtime is available (%s).", runtime), map[string]any{"runtime": runtime})
}

func configFilesCheck(userPath string, projectPath string) Check {
	details := map[string]any{}
	if strings.TrimSpace(userPath) != "" {
		details["userConfigPath"] = userPath
	}
	if strings.TrimSpace(projectPath) != "" {
		details["projectConfigPath"] = projectPath
	}
	if len(details) == 0 {
		return check("config.files", "Config files", StatusWarn, "No explicit Splice config files were inspected.", nil)
	}
	return check("config.files", "Config files", StatusPass, "Splice config file inputs are available for inspection.", details)
}

func providerConfigCheck(profile config.ProviderProfile) Check {
	if emptyProviderProfile(profile) {
		return check("provider.config", "Provider config", StatusFail, "No LLM provider is configured.", map[string]any{"help": "Set a provider in config or environment."})
	}
	// Report credential PRESENCE, never the value. Reported under a non-sensitive
	// key ("credentialConfigured"): the prior "apiKey" key was itself sensitive, so
	// check()'s redaction scrubbed the indicator to [REDACTED] — making "set"/"not
	// set" invisible. A profile can authenticate via a raw auth-header value instead
	// of APIKey, and both are trimmed so a whitespace-only value reads "not set"
	// (matching ProviderSnapshot.APIKeySet).
	credential := "not set"
	if strings.TrimSpace(profile.APIKey) != "" || strings.TrimSpace(profile.AuthHeaderValue) != "" {
		credential = "set"
	}
	details := map[string]any{
		"name":                 profile.Name,
		"provider":             profile.ProviderKind,
		"baseURL":              profile.BaseURL,
		"model":                profile.Model,
		"credentialConfigured": credential,
	}
	// A remote provider with no credential cannot make a request, so doctor must NOT
	// report it as healthy — otherwise "Overall: pass" gives a false all-clear for the
	// one tool meant to verify keys. Keyless local providers (loopback base_url, e.g.
	// Ollama/LM Studio) legitimately need no key, so they stay a pass. (AUDIT-H9)
	if credential == "not set" && !localProviderBaseURL(profile.BaseURL) {
		return check("provider.config", "Provider config", StatusFail,
			fmt.Sprintf("No API key configured for %s. Run `splice auth` or `splice setup`, or set the provider's API key environment variable.", providerName(profile)),
			details)
	}
	return check("provider.config", "Provider config", StatusPass, fmt.Sprintf("Provider config loaded for %s.", providerName(profile)), details)
}

// hydrateStoredKey loads a stored API key into a keyless profile, as a run does.
// Any failure returns the profile unchanged, so storedKeyReachabilityCheck
// still reports the cause.
func hydrateStoredKey(profile config.ProviderProfile, userConfig string, open config.KeyStoreOpener) config.ProviderProfile {
	if !profile.APIKeyStored || strings.TrimSpace(profile.APIKey) != "" || strings.TrimSpace(profile.AuthHeaderValue) != "" {
		return profile
	}
	if open == nil {
		open = config.ProviderKeyStoreOpener
	}
	if strings.TrimSpace(profile.Name) == "" || strings.TrimSpace(userConfig) == "" {
		return profile
	}
	store, _, err := open(filepath.Dir(strings.TrimSpace(userConfig)))
	if err != nil {
		return profile
	}
	hydrated, err := config.ApplyStoredAPIKey(profile, store)
	if err != nil {
		return profile
	}
	return hydrated
}

// storedKeyReachabilityCheck turns the silent stored-key failure mode into a
// loud diagnosis. A profile with APIKeyStored=true that resolves keyless means
// the credential store was marked as holding a key but the read found none or
// failed. That state previously surfaced only as "No API key configured" from
// the provider.config check, which hides the real cause (for example a login
// keychain that is not reachable because the process runs under a different
// HOME). Never prints key material: the read result is presence only.
func storedKeyReachabilityCheck(profile config.ProviderProfile, userConfig string, open config.KeyStoreOpener) Check {
	if !profile.APIKeyStored || strings.TrimSpace(profile.APIKey) != "" || strings.TrimSpace(profile.AuthHeaderValue) != "" {
		// No stored-key promise, or the key already resolved. Nothing to verify.
		return check("provider.storedkey", "Stored key reachability", StatusPass,
			"No stored-key check is needed for this provider profile.", nil)
	}
	if open == nil {
		open = config.ProviderKeyStoreOpener
	}
	name := strings.TrimSpace(profile.Name)
	dir := filepath.Dir(strings.TrimSpace(userConfig))
	if name == "" || dir == "" {
		return check("provider.storedkey", "Stored key reachability", StatusWarn,
			"Cannot inspect the credential store: the provider name or user config path is unknown.",
			map[string]any{"provider": name})
	}
	store, backend, err := open(dir)
	if err != nil {
		return check("provider.storedkey", "Stored key reachability", StatusWarn,
			fmt.Sprintf("The credential store for %s could not be opened: %v. Stored-key auth cannot resolve until this is fixed.", name, err),
			map[string]any{"provider": name, "backend": backend})
	}
	_, keyFound, err := store.Get(name)
	if err != nil {
		return check("provider.storedkey", "Stored key reachability", StatusWarn,
			fmt.Sprintf("Reading the stored key for %s from the %s backend failed: %v. The profile is marked as stored-key auth, so runtime lookups will keep failing until the key is reachable or re-entered with `splice auth login`.", name, backend, err),
			map[string]any{"provider": name, "backend": backend, "storedKeyMarked": true})
	}
	if !keyFound {
		return check("provider.storedkey", "Stored key reachability", StatusWarn,
			fmt.Sprintf("The profile for %s is marked for stored-key auth, but the %s backend holds no key for it. The key may live in a different user context (check HOME) or was removed. Run `splice auth login` to store it again.", name, backend),
			map[string]any{"provider": name, "backend": backend, "storedKeyMarked": true, "keyFound": false})
	}
	return check("provider.storedkey", "Stored key reachability", StatusPass,
		fmt.Sprintf("A stored key for %s is present in the %s backend.", name, backend),
		map[string]any{"provider": name, "backend": backend, "keyFound": true})
}

// localProviderBaseURL reports whether the configured base_url is a loopback host
// (a keyless local provider like Ollama/LM Studio), which needs no API key.
func localProviderBaseURL(baseURL string) bool {
	u := strings.TrimSpace(baseURL)
	if u == "" {
		return false
	}
	if parsed, err := url.Parse(u); err == nil && parsed.Host != "" {
		host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
		if host == "localhost" || strings.HasSuffix(host, ".localhost") {
			return true
		}
		if addr, err := netip.ParseAddr(host); err == nil {
			return addr.IsLoopback()
		}
	}
	return false
}

func providerModelCheck(profile config.ProviderProfile) Check {
	if emptyProviderProfile(profile) {
		return check("provider.model", "Provider model", StatusWarn, "Model validity was skipped because provider config is unavailable.", nil)
	}
	if strings.TrimSpace(profile.Model) == "" {
		return check("provider.model", "Provider model", StatusFail, "Provider model is required.", map[string]any{"provider": providerName(profile)})
	}
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		return check("provider.model", "Provider model", StatusFail, "Model registry could not be loaded: "+err.Error(), nil)
	}
	model, err := registry.Require(profile.Model)
	if err != nil {
		if profile.ProviderKind == config.ProviderKindOpenAICompatible || profile.ProviderKind == config.ProviderKindAnthropicCompat {
			return check("provider.model", "Provider model", StatusWarn, fmt.Sprintf("Custom %s model was not found in the Splice registry; runtime will pass it through to the configured provider. Run `splice doctor --connectivity` to validate the endpoint and auth.", profile.ProviderKind), map[string]any{"model": profile.Model, "provider": providerName(profile)})
		}
		return check("provider.model", "Provider model", StatusFail, "Provider model is invalid: "+err.Error(), map[string]any{"model": profile.Model})
	}
	if !model.AllowsProvider(toModelProvider(profile)) {
		return check("provider.model", "Provider model", StatusFail, fmt.Sprintf("Model %s is not available for provider %s.", model.ID, providerName(profile)), map[string]any{"model": model.ID, "provider": providerName(profile)})
	}
	return check("provider.model", "Provider model", StatusPass, fmt.Sprintf("Model %s resolves to %s.", model.ID, model.Provider), map[string]any{
		"modelId":      model.ID,
		"apiModel":     model.APIModel,
		"provider":     model.Provider,
		"capabilities": model.Capabilities,
	})
}

func connectivityCheck(profile config.ProviderProfile, enabled bool, modelStatus Status, health *providerhealth.Result) Check {
	if !enabled {
		if emptyProviderProfile(profile) || modelStatus == StatusFail {
			return check("provider.connectivity", "Provider connectivity", StatusWarn, "Connectivity check was skipped because provider runtime did not resolve.", nil)
		}
		return check("provider.connectivity", "Provider connectivity", StatusWarn, "Connectivity probe skipped. Run `splice doctor --connectivity` to probe the provider endpoint.", map[string]any{"baseURL": profile.BaseURL})
	}
	if health != nil {
		if providerCheck := health.PrimaryCheck(); providerCheck != nil {
			return check("provider.connectivity", "Provider connectivity", doctorStatus(providerCheck.Status), providerCheck.Message, providerCheck.Details)
		}
		return check("provider.connectivity", "Provider connectivity", doctorStatus(health.Status), "Provider health probe completed without a connectivity check.", map[string]any{"healthStatus": health.Status})
	}
	if emptyProviderProfile(profile) || modelStatus == StatusFail {
		return check("provider.connectivity", "Provider connectivity", StatusWarn, "Connectivity check was skipped because provider runtime did not resolve.", nil)
	}
	// Reached only when a caller sets Connectivity true but does not supply a
	// ProviderHealth probe result. Both shipped callers (internal/cli's
	// `splice doctor --connectivity` and the TUI's doctorOptions) always probe
	// and pass a result when connectivity is requested and the profile
	// resolves, so this branch is a caller bug, not an unbuilt backend. The
	// message used to claim "not wired in the Go doctor backend yet", which
	// was false: providerhealth.Probe is wired and working; the caller just
	// didn't run it.
	return check("provider.connectivity", "Provider connectivity", StatusWarn, "Connectivity was requested, but no provider health probe result was supplied.", map[string]any{"baseURL": profile.BaseURL})
}

func doctorStatus(status providerhealth.Status) Status {
	switch status {
	case providerhealth.StatusPass:
		return StatusPass
	case providerhealth.StatusFail:
		return StatusFail
	default:
		return StatusWarn
	}
}

func emptyProviderProfile(profile config.ProviderProfile) bool {
	return !config.HasProviderProfile(profile)
}

func check(id string, label string, status Status, message string, details map[string]any) Check {
	redacted := redaction.RedactValue(map[string]any{
		"id":      id,
		"label":   label,
		"status":  string(status),
		"message": message,
		"details": details,
	}, redaction.Options{}).(map[string]any)
	out := Check{
		ID:      redacted["id"].(string),
		Label:   redacted["label"].(string),
		Status:  Status(redacted["status"].(string)),
		Message: redacted["message"].(string),
	}
	if detailsValue, ok := redacted["details"].(map[string]any); ok && len(detailsValue) > 0 {
		out.Details = detailsValue
	}
	return out
}

func providerName(profile config.ProviderProfile) string {
	if strings.TrimSpace(profile.Name) != "" {
		return strings.TrimSpace(profile.Name)
	}
	if strings.TrimSpace(string(profile.ProviderKind)) != "" {
		return strings.TrimSpace(string(profile.ProviderKind))
	}
	return strings.TrimSpace(profile.Provider)
}

func toModelProvider(profile config.ProviderProfile) modelregistry.ProviderKind {
	switch profile.ProviderKind {
	case config.ProviderKindAnthropic, config.ProviderKindAnthropicCompat:
		return modelregistry.ProviderAnthropic
	case config.ProviderKindGoogle:
		return modelregistry.ProviderGoogle
	case config.ProviderKindOpenAICompatible:
		return modelregistry.ProviderOpenAICompatible
	default:
		return modelregistry.ProviderOpenAI
	}
}

func formatDetails(details map[string]any) string {
	if len(details) == 0 {
		return ""
	}
	keys := make([]string, 0, len(details))
	for key := range details {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(details))
	for _, key := range keys {
		value := details[key]
		parts = append(parts, fmt.Sprintf("%s: %s", redaction.RedactString(key, redaction.Options{}), formatDetailValue(redaction.RedactValue(value, redaction.Options{}))))
	}
	return strings.Join(parts, " | ")
}

// formatDetailValue renders a detail value for human reading. A nested map (e.g. the
// lsp.servers missing-tools list or config.validation results) was previously printed
// with %v, leaking Go's `map[k:v ...]` syntax into user output; render maps as sorted
// "k: v" entries and slices as a comma list instead. (AUDIT-H8)
func formatDetailValue(value any) string {
	if value == nil {
		return ""
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Map:
		entries := make([]string, 0, rv.Len())
		for _, k := range rv.MapKeys() {
			entries = append(entries, fmt.Sprintf("%v: %v", k.Interface(), rv.MapIndex(k).Interface()))
		}
		sort.Strings(entries)
		return strings.Join(entries, "; ")
	case reflect.Slice, reflect.Array:
		if rv.Type().Elem().Kind() == reflect.Uint8 { // []byte
			return fmt.Sprintf("%v", value)
		}
		parts := make([]string, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			parts = append(parts, fmt.Sprintf("%v", rv.Index(i).Interface()))
		}
		return strings.Join(parts, ", ")
	case reflect.String:
		return rv.String()
	default:
		return fmt.Sprintf("%v", value)
	}
}

// configValidationFile is one config file to validate. root bounds the read:
// the project file is workspace-supplied, so it must stay inside the workspace.
// The user file is user-owned and has no root.
type configValidationFile struct {
	path string
	root string
}

func configValidationCheck(userPath string, projectPath string) Check {
	files := make([]configValidationFile, 0, 2)
	if strings.TrimSpace(userPath) != "" {
		files = append(files, configValidationFile{path: userPath})
	}
	if strings.TrimSpace(projectPath) != "" {
		// The project config sits at <root>/.splice/config.json.
		files = append(files, configValidationFile{path: projectPath, root: filepath.Dir(filepath.Dir(projectPath))})
	}
	if len(files) == 0 {
		return check("config.validation", "Config validation", StatusWarn, "No Splice config files were available to validate.", nil)
	}

	status := StatusPass
	issueCount := 0
	details := map[string]any{}
	for _, file := range files {
		path := file.path
		data, readErr := readConfigForValidation(file)
		if readErr != nil {
			// A genuinely-missing path is configFilesCheck's job to report, and a
			// "missing" path must stay a skip — so a not-exist read error is ignored
			// here. Any OTHER read error (permissions, is-a-directory) means a
			// present-but-unreadable config that would otherwise silently pass
			// validation, so surface it as a failing per-path detail.
			if os.IsNotExist(readErr) {
				continue
			}
			details[path] = map[string]any{"error": "unreadable: " + readErr.Error()}
			status = StatusFail
			issueCount++
			continue
		}
		if line, col, ok := jsonParsePosition(data); ok {
			_, issues := config.ValidateBytes(data)
			errMsg := ""
			if len(issues) > 0 {
				errMsg = issues[0].Message
			}
			details[path] = map[string]any{"line": line, "col": col, "error": errMsg}
			status = StatusFail
			issueCount++
			continue
		}
		_, issues := config.ValidateBytes(data)
		if len(issues) == 0 {
			continue
		}
		messages := make([]string, 0, len(issues))
		for _, issue := range issues {
			messages = append(messages, issue.Message)
		}
		details[path] = map[string]any{"issues": messages}
		status = StatusFail
		issueCount += len(issues)
	}

	if status == StatusPass {
		return check("config.validation", "Config validation", StatusPass, "Splice config files parsed and validated successfully.", nil)
	}
	return check("config.validation", "Config validation", StatusFail, fmt.Sprintf("Splice config validation found %d issue(s).", issueCount), details)
}

// readConfigForValidation reads one config file. A project file goes through
// the bounded reader, so a FIFO, a device, an oversized file, or a symlink that
// leaves the workspace fails the read instead of stalling or exhausting memory.
func readConfigForValidation(file configValidationFile) ([]byte, error) {
	if file.root == "" {
		return os.ReadFile(file.path)
	}
	return projectfile.Read(file.path, file.root)
}

// jsonParsePosition reports whether data fails to parse as JSON and, if so, the
// 1-based line/col of the failure using the concrete json error offset.
//
// The probe is a config.FileConfig (not `any`) so that a structurally-valid
// document with a wrong field type (e.g. {"maxTurns":"twelve"}) surfaces a
// *json.UnmarshalTypeError carrying the offset. FileConfig.UnmarshalJSON returns
// the underlying json error unchanged, preserving that offset. Documents that
// are structurally valid AND type-correct unmarshal cleanly (ok=false) and fall
// through to the semantic ValidateBytes branch.
func jsonParsePosition(data []byte) (int, int, bool) {
	var probe config.FileConfig
	err := json.Unmarshal(data, &probe)
	if err == nil {
		return 0, 0, false
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		line, col := offsetToLineCol(data, syntaxErr.Offset)
		return line, col, true
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		line, col := offsetToLineCol(data, typeErr.Offset)
		return line, col, true
	}
	// Non-positional JSON error (e.g. unexpected EOF without offset): do not
	// fabricate a (1,1) position. Returning ok=false routes the error through the
	// no-position ValidateBytes path so it is reported without a fake line/col.
	return 0, 0, false
}

func passFail(ok bool) string {
	if ok {
		return "pass"
	}
	return "fail"
}

// offsetToLineCol converts a byte offset (as reported by *json.SyntaxError /
// *json.UnmarshalTypeError) into a 1-based line and column. Offsets out of range
// are clamped into [0, len(data)].
func offsetToLineCol(data []byte, offset int64) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > int64(len(data)) {
		offset = int64(len(data))
	}
	line := 1
	col := 1
	for index := int64(0); index < offset; index++ {
		if data[index] == '\n' {
			line++
			col = 1
			continue
		}
		col++
	}
	return line, col
}

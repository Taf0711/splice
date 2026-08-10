# OAuth and provider login

Splice supports API keys, selected OAuth logins, local model servers, and
compatible custom endpoints.

Use an API key when the provider documents that path for third-party clients.
A consumer chat subscription is not always an API credential.

## Inspect login state

```bash
splice auth status
splice auth status <provider>
```

The status command reports login presence and expiry. It never prints the token.

Use these commands to manage a login:

```bash
splice auth login <provider>
splice auth login <provider> --device
splice auth refresh <provider>
splice auth logout <provider>
```

## OpenRouter

OpenRouter supports a browser flow that creates an API key for Splice.

```bash
splice auth openrouter
```

You can also select OpenRouter in the setup flow. Splice stores the created key
with the provider profile.

## ChatGPT

Splice includes a browser login for supported ChatGPT accounts:

```bash
splice auth chatgpt
```

This route uses the provider's Codex service rather than the standard OpenAI API.
Provider access rules can change. Run `splice auth status chatgpt` and
`splice doctor` when the route stops.

Use `OPENAI_API_KEY` with the normal OpenAI provider when you need the standard
OpenAI API and its documented billing path.

## xAI

The xAI preset needs an OAuth client identity. It is disabled by default.

Enable the built-in preset only when your account and provider terms permit it:

```bash
export SPLICE_OAUTH_ALLOW_PRESETS=1
splice auth login xai
```

You can also supply your own OAuth application settings through
`SPLICE_OAUTH_XAI_*` variables.

## Hugging Face

Register a public OAuth application with Hugging Face. Then set its client ID
and start the login:

```bash
export SPLICE_OAUTH_HUGGINGFACE_CLIENT_ID=YOUR_CLIENT_ID
splice auth login huggingface
```

Use `--device` for a headless host when the provider supports device login.

## Custom OAuth or OIDC

For a custom provider named `acme`, set its OAuth values and start the login:

```bash
export SPLICE_OAUTH_ACME_CLIENT_ID=YOUR_CLIENT_ID
export SPLICE_OAUTH_ACME_AUTHORIZE_URL=https://provider.example/authorize
export SPLICE_OAUTH_ACME_TOKEN_URL=https://provider.example/token
export SPLICE_OAUTH_ACME_SCOPES="openid profile"
splice auth login acme
```

Available variable groups include:

```text
SPLICE_OAUTH_<NAME>_CLIENT_ID
SPLICE_OAUTH_<NAME>_CLIENT_SECRET
SPLICE_OAUTH_<NAME>_AUTHORIZE_URL
SPLICE_OAUTH_<NAME>_TOKEN_URL
SPLICE_OAUTH_<NAME>_DEVICE_URL
SPLICE_OAUTH_<NAME>_ISSUER_URL
SPLICE_OAUTH_<NAME>_SCOPES
SPLICE_OAUTH_<NAME>_FLOW
```

Use HTTPS endpoints. Loopback callback addresses are the only local exception.

Splice can refresh a supported bearer token and retry one unauthorized request.
Without a login, the provider profile uses its configured API key.

## Headless login

Use device login when the provider offers it:

```bash
splice auth login <provider> --device
```

Splice can select device login automatically on a host without a browser. Set
`SPLICE_OAUTH_DEVICE=1` to request this mode for the process.

## Token storage

macOS uses the system keyring by default. Other platforms use an encrypted local
file under the Splice configuration directory.

Set this variable to choose another token path:

```bash
export SPLICE_OAUTH_TOKENS_PATH=/secure/path/oauth-tokens.json
```

Set `SPLICE_OAUTH_STORAGE=file` only when you accept a plaintext file with mode
`0600`.

MCP OAuth tokens use the same credential system. You can select a separate MCP
token path with `SPLICE_MCP_OAUTH_TOKENS_PATH`.

## Subscription limits

A ChatGPT or Claude web subscription does not automatically grant standard API
access. Use only a login path that the provider permits for third-party tools.

Splice does not support direct Claude subscription login. Use an Anthropic API
key for the Anthropic provider.

Splice can connect to a local OpenAI-compatible or Anthropic-compatible proxy.
The proxy remains a separate trust boundary. Review its source, storage,
network behavior, and vendor terms before use.

Point a custom provider profile at a loopback URL when you operate such a proxy.
Do not expose an unauthenticated proxy on a public network.

## Recommended choices

- Use a provider API key for the most stable setup.
- Use the built-in OpenRouter login when you want an OpenRouter key.
- Use the ChatGPT command only for its supported Codex route.
- Use your own OAuth application for a custom provider.
- Use a local model when repository data must stay off a cloud provider.

Read [Configuration](CONFIGURATION.md) for profile and policy precedence.

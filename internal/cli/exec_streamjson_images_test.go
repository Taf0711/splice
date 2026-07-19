package cli

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

// TestRunExecStreamJSONImageReachesAgent locks the end-to-end wiring: a base64
// image carried on a stream-json `message` event, fed via stdin on a vision
// model, must reach agent.Options.Images (and therefore the provider request's
// user turn). Before the fix, stream-json images were parsed and validated but
// silently dropped — never threaded into the agent run.
func TestRunExecStreamJSONImageReachesAgent(t *testing.T) {
	cwd := t.TempDir()

	// A minimal PNG, base64-encoded for the stream-json image payload.
	png := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
	}
	encoded := base64.StdEncoding.EncodeToString(png)
	input := `{"schemaVersion":2,"type":"message","role":"user","content":"describe this","images":[{"mediaType":"image/png","data":"` + encoded + `"}]}` + "\n"

	provider := newExecStageAwareProvider(execStageProviderOptions{})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{
		"exec",
		"--cwd", cwd,
		"--input-format", "stream-json",
		// gpt-4.1 is a registry-known vision model, so the gate keeps the image.
		"--model", "gpt-4.1",
	}, &stdout, &stderr, appDeps{
		stdin: strings.NewReader(input),
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveConfig: func(_ string, overrides config.Overrides) (config.ResolvedConfig, error) {
			return execStageResolvedConfig(overrides.Provider.Model, 3), nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return provider, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}

	images := provider.lastImages()
	if len(images) != 1 {
		t.Fatalf("expected 1 image to reach the agent run, got %d (stderr=%q)", len(images), stderr.String())
	}
	if images[0].MediaType != "image/png" {
		t.Fatalf("image media type = %q, want image/png", images[0].MediaType)
	}
	if !bytes.Equal(images[0].Data, png) {
		t.Fatalf("image bytes = %v, want decoded png", images[0].Data)
	}
}

// TestRunExecStreamJSONImageOnlyMessageProceeds locks that an IMAGE-ONLY turn
// (a message event with EMPTY content but at least one image) is accepted: the
// run proceeds with an empty prompt and the image still reaches the agent. Before
// the fix, ResolvePrompt's "must include at least one prompt or user message
// event" error was returned before images were considered, rejecting the turn.
func TestRunExecStreamJSONImageOnlyMessageProceeds(t *testing.T) {
	cwd := t.TempDir()

	png := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
	}
	encoded := base64.StdEncoding.EncodeToString(png)
	// Empty content, one image: the image-only turn.
	input := `{"schemaVersion":2,"type":"message","role":"user","content":"","images":[{"mediaType":"image/png","data":"` + encoded + `"}]}` + "\n"

	provider := newExecStageAwareProvider(execStageProviderOptions{})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{
		"exec",
		"--cwd", cwd,
		"--input-format", "stream-json",
		"--model", "gpt-4.1",
	}, &stdout, &stderr, appDeps{
		stdin: strings.NewReader(input),
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveConfig: func(_ string, overrides config.Overrides) (config.ResolvedConfig, error) {
			return execStageResolvedConfig(overrides.Provider.Model, 3), nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return provider, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("image-only turn rejected: exit code %d (want %d): %s", exitCode, exitSuccess, stderr.String())
	}

	images := provider.lastImages()
	if len(images) != 1 {
		t.Fatalf("expected 1 image to reach the agent run, got %d (stderr=%q)", len(images), stderr.String())
	}
	if images[0].MediaType != "image/png" || !bytes.Equal(images[0].Data, png) {
		t.Fatalf("image not threaded: %#v", images[0])
	}
}

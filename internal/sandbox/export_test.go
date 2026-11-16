package sandbox

// CredentialFileDenyPaths gives the external stage policy test
// (stage_policy_darwin_test.go, package sandbox_test) the real credential
// list, so the test loops over the shipped entries, not a copy that can drift.
var CredentialFileDenyPaths = credentialFileDenyPaths

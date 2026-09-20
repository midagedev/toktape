package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFromMissingFileIsNotAnError(t *testing.T) {
	c, err := LoadFrom(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatalf("LoadFrom on a missing file: %v", err)
	}
	if c.Token != "" || c.FirstPublishWarningSeen || c.PublishText != nil || c.HostLabel != "" {
		t.Errorf("a missing file produced %+v, want the zero config", c)
	}
	// A machine that never said anything publishes its text, per §9.3.
	if !c.WantsText() {
		t.Error("WantsText() = false on an unconfigured machine, want true")
	}
}

func TestLoadFrom(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	write(t, path, `# a comment
token = "tk_live_abc#123"   # trailing comment, and a '#' inside the token

first_publish_warning_seen = true
publish_text = false
host_label = "workstation"
`)
	c, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if c.Token != "tk_live_abc#123" {
		t.Errorf("Token = %q: a '#' inside a quoted value is not a comment", c.Token)
	}
	if !c.FirstPublishWarningSeen {
		t.Error("FirstPublishWarningSeen = false")
	}
	if c.PublishText == nil || *c.PublishText {
		t.Errorf("PublishText = %v, want an explicit false", c.PublishText)
	}
	if c.WantsText() {
		t.Error("WantsText() = true although the machine opted out")
	}
	if c.HostLabel != "workstation" {
		t.Errorf("HostLabel = %q", c.HostLabel)
	}
}

// The file holds an opt-out, so a line this parser cannot read has to stop it.
// Skipping one would publish the body on a machine whose owner turned it off,
// and it would do so silently — which is the only failure mode here that
// matters.
func TestLoadFromRefusesWhatItCannotRead(t *testing.T) {
	for _, c := range []struct {
		name, body, wants string
	}{
		{"a line that is not name = value", "token\n", "not `name = value`"},
		{"a key nobody reads", "publish_txt = false\n", `unknown key "publish_txt"`},
		{"a boolean that is not one", "publish_text = no\n", "want true or false"},
		{"an unterminated quote", `token = "abc` + "\n", "not a quoted string"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			write(t, path, c.body)
			_, err := LoadFrom(path)
			if err == nil {
				t.Fatalf("LoadFrom accepted %q", c.body)
			}
			if !strings.Contains(err.Error(), c.wants) {
				t.Errorf("error = %q, want it to mention %q", err, c.wants)
			}
			if !strings.Contains(err.Error(), "line 1") {
				t.Errorf("error = %q, want it to name the line", err)
			}
		})
	}
}

func TestSaveToRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.toml")
	no := false
	in := &Config{Token: "tk_live_abc", FirstPublishWarningSeen: true, PublishText: &no, HostLabel: "the rig"}
	if err := SaveTo(path, in); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// A token must never exist on disk world-readable, not even between a
	// create and a chmod.
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600: the file holds a credential", perm)
	}

	out, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if out.Token != in.Token || out.HostLabel != in.HostLabel || !out.FirstPublishWarningSeen {
		t.Errorf("round trip lost a field: %+v", out)
	}
	if out.PublishText == nil || *out.PublishText {
		t.Errorf("PublishText = %v, want the explicit false back", out.PublishText)
	}
}

// An unset publish_text must not come back as false: absence is "the
// default" and false is a decision, and writing the file must not turn one
// into the other.
func TestSaveToKeepsUnsetUnset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := SaveTo(path, &Config{Token: "tk"}); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"publish_text", "host_label", "first_publish_warning_seen"} {
		if strings.Contains(string(body), key) {
			t.Errorf("unset key %q was written:\n%s", key, body)
		}
	}
	out, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if out.PublishText != nil {
		t.Errorf("PublishText = %v after a round trip through an unset value", *out.PublishText)
	}
}

// The profile keys round-trip the way host_label does: parsed by set,
// written by render, and absent when unset so a default never turns into a
// decision.
func TestProfileKeysRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	write(t, path, `host_label = "the rig"
profile_name = "Lab Rat"
profile_link = "https://github.com/example"
profile_avatar = "~/pic.png"
`)
	c, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if c.ProfileName != "Lab Rat" || c.ProfileLink != "https://github.com/example" || c.ProfileAvatar != "~/pic.png" {
		t.Errorf("profile did not survive the load: %+v", c)
	}

	out := filepath.Join(t.TempDir(), "out.toml")
	if err := SaveTo(out, c); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	back, err := LoadFrom(out)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if *back != *c {
		t.Errorf("round trip lost a field: %+v became %+v", c, back)
	}

	// Unset profile keys are not written: nothing about the author travels
	// by default, and the file must not invent it.
	plain := filepath.Join(t.TempDir(), "plain.toml")
	if err := SaveTo(plain, &Config{Token: "tk"}); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	body, err := os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"profile_name", "profile_link", "profile_avatar"} {
		if strings.Contains(string(body), key) {
			t.Errorf("unset key %q was written:\n%s", key, body)
		}
	}
}

// The service key round-trips the way host_label does: parsed by set, written
// by render, absent when unset — and it decides where the journal token may
// travel (cmd/toktape/service.go), which is why its doc comment states that
// rule.
func TestServiceKeyRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	write(t, path, "service = \"https://tapes.example.com\"\n")
	c, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if c.Service != "https://tapes.example.com" {
		t.Errorf("Service = %q", c.Service)
	}

	out := filepath.Join(t.TempDir(), "out.toml")
	if err := SaveTo(out, c); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	back, err := LoadFrom(out)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if back.Service != c.Service {
		t.Errorf("round trip lost the service: %q became %q", c.Service, back.Service)
	}

	// Unset is not written: a machine on the hosted service has no key, and
	// the file must not invent a destination for the token.
	plain := filepath.Join(t.TempDir(), "plain.toml")
	if err := SaveTo(plain, &Config{Token: "tk"}); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	body, err := os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "service") {
		t.Errorf("unset key \"service\" was written:\n%s", body)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The bio key round-trips the way the other profile keys do: parsed by set,
// written by render, and absent when unset so nothing about the author is
// invented (TTP-127).
func TestProfileBioRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	write(t, path, "profile_bio = \"Line one.\\n\\nLine two.\"\n")
	c, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if c.ProfileBio != "Line one.\n\nLine two." {
		t.Errorf("bio did not survive the load: %q", c.ProfileBio)
	}

	out := filepath.Join(t.TempDir(), "out.toml")
	if err := SaveTo(out, c); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	back, err := LoadFrom(out)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if back.ProfileBio != c.ProfileBio {
		t.Errorf("round trip lost the bio: %q became %q", c.ProfileBio, back.ProfileBio)
	}

	// Unset means the key is not written: a bio is opt-in, and the file
	// must not invent one.
	plain := filepath.Join(t.TempDir(), "plain.toml")
	if err := SaveTo(plain, &Config{Token: "tk"}); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	body, err := os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "profile_bio") {
		t.Errorf("unset key \"profile_bio\" was written:\n%s", body)
	}
}

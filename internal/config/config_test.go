package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

func TestLoadAcceptsCompleteServeConfiguration(t *testing.T) {
	temporary := t.TempDir()
	dataDir := filepath.Join(temporary, "data")
	docRoot := filepath.Join(temporary, "frontend")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(docRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(temporary, "mia.toml")
	content := "[main]\ndata_dir = \"" + dataDir + "\"\ndoc_root = \"" + docRoot + "\"\npublic_url = \"https://mia.example.test\"\n[openai]\napi_key = \"key\"\n[mistral]\napi_key = \"key\"\n[smtp]\nhost = \"smtp.example.test\"\nsender_email = \"mia@example.test\"\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	AddFlags(flags)
	if err := flags.Set("config", configPath); err != nil {
		t.Fatal(err)
	}
	configuration, err := Load(flags, true)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.HTTP.Listen != "127.0.0.1:9900" {
		t.Fatalf("listen = %q", configuration.HTTP.Listen)
	}
}

func TestLoadRejectsUnknownConfigurationKey(t *testing.T) {
	temporary := t.TempDir()
	configPath := filepath.Join(temporary, "mia.toml")
	if err := os.WriteFile(configPath, []byte("[unknown]\nvalue = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	AddFlags(flags)
	if err := flags.Set("config", configPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(flags, false); err == nil {
		t.Fatal("Load accepted an unknown key")
	}
}

func TestExampleConfigurationDocumentsEverySchemaKey(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "mia.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	documented := documentedExampleKeys(string(content))
	schema := make(map[string]struct{}, len(settings))
	for _, setting := range settings {
		schema[setting.key] = struct{}{}
		if _, ok := documented[setting.key]; !ok {
			t.Errorf("mia.example.toml does not document %q", setting.key)
		}
	}
	for key := range documented {
		if _, ok := schema[key]; !ok {
			t.Errorf("mia.example.toml documents unknown key %q", key)
		}
	}
}

func documentedExampleKeys(content string) map[string]struct{} {
	keys := make(map[string]struct{})
	table := ""
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			table = strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			continue
		}
		if table == "" || !strings.Contains(line, "=") {
			continue
		}
		key := strings.TrimSpace(strings.SplitN(line, "=", 2)[0])
		if key != "" && !strings.ContainsAny(key, " \t") {
			keys[table+"."+key] = struct{}{}
		}
	}
	return keys
}

func TestValidateConfigFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mia.toml")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0o600, 0o640} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if err := validateConfigFile(path); err != nil {
			t.Errorf("mode %04o rejected: %v", mode, err)
		}
	}
	for _, mode := range []os.FileMode{0o660, 0o650, 0o604} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if err := validateConfigFile(path); err == nil {
			t.Errorf("mode %04o accepted", mode)
		}
	}
}

func TestLoadNormalizesTrailingPublicURLSlash(t *testing.T) {
	configuration := Config{}
	configuration.Main.PublicURL = "https://mia.example.test/"
	if err := normalizePublicURL(&configuration); err != nil {
		t.Fatal(err)
	}
	if configuration.Main.PublicURL != "https://mia.example.test" {
		t.Fatalf("public URL = %q", configuration.Main.PublicURL)
	}
}

func TestSupportedModelRegistryRejectsUnknownValues(t *testing.T) {
	if !supportedModel("gpt-5.6-terra") {
		t.Fatal("default model is unsupported")
	}
	if supportedModel("unknown-model") || supportedModel("") {
		t.Fatal("unsupported model accepted")
	}
}

func TestNormalizeTrustedProxyCIDRsTrimsAndRejectsEmptyValues(t *testing.T) {
	configuration := Config{}
	configuration.HTTP.TrustedProxyCIDRs = []string{"10.0.0.0/8, 192.168.0.0/16"}
	if err := normalizeListSettings(&configuration); err != nil {
		t.Fatal(err)
	}
	if strings.Join(configuration.HTTP.TrustedProxyCIDRs, ",") != "10.0.0.0/8,192.168.0.0/16" {
		t.Fatalf("normalized CIDRs = %v", configuration.HTTP.TrustedProxyCIDRs)
	}
	configuration.HTTP.TrustedProxyCIDRs = []string{"10.0.0.0/8,,192.168.0.0/16"}
	if err := normalizeListSettings(&configuration); err == nil {
		t.Fatal("empty CIDR accepted")
	}
}

func TestLoadNormalizesTrustedProxyCIDRsFromEnvironmentAndFlags(t *testing.T) {
	for name, configure := range map[string]func(*pflag.FlagSet){
		"environment": func(_ *pflag.FlagSet) { t.Setenv("MIA_HTTP_TRUSTED_PROXY_CIDRS", "10.0.0.0/8, 192.168.0.0/16") },
		"flag": func(flags *pflag.FlagSet) {
			if err := flags.Set("http-trusted-proxy-cidrs", "10.0.0.0/8, 192.168.0.0/16"); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			temporary := t.TempDir()
			dataDir := filepath.Join(temporary, "data")
			docRoot := filepath.Join(temporary, "frontend")
			if err := os.Mkdir(dataDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(docRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(temporary, "mia.toml")
			content := "[main]\ndata_dir = \"" + dataDir + "\"\ndoc_root = \"" + docRoot + "\"\npublic_url = \"https://mia.example.test\"\n[openai]\napi_key = \"key\"\n[mistral]\napi_key = \"key\"\n[smtp]\nhost = \"smtp.example.test\"\nsender_email = \"mia@example.test\"\n"
			if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
			AddFlags(flags)
			if err := flags.Set("config", configPath); err != nil {
				t.Fatal(err)
			}
			configure(flags)
			configuration, err := Load(flags, true)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(configuration.HTTP.TrustedProxyCIDRs, ",") != "10.0.0.0/8,192.168.0.0/16" {
				t.Fatalf("CIDRs = %v", configuration.HTTP.TrustedProxyCIDRs)
			}
		})
	}
}

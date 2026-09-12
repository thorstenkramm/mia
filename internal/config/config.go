// Package config loads and validates MIA operator configuration.
package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/identity"
)

// Config is the complete MIA configuration schema.
type Config struct {
	CookiePolicy httpserver.CookiePolicy `mapstructure:"-"`
	Main         struct {
		DataDir   string `mapstructure:"data_dir"`
		DocRoot   string `mapstructure:"doc_root"`
		PublicURL string `mapstructure:"public_url"`
	} `mapstructure:"main"`
	HTTP struct {
		Listen            string   `mapstructure:"listen"`
		TrustedProxyCIDRs []string `mapstructure:"trusted_proxy_cidrs"`
		SocketGroup       string   `mapstructure:"socket_group"`
	} `mapstructure:"http"`
	Log struct {
		File   string `mapstructure:"file"`
		Level  string `mapstructure:"level"`
		Format string `mapstructure:"format"`
	} `mapstructure:"log"`
	OpenAI struct {
		APIKey    string `mapstructure:"api_key"`
		ChatModel string `mapstructure:"chat_model"`
		JobModel  string `mapstructure:"job_model"`
	} `mapstructure:"openai"`
	Mistral struct {
		APIKey string `mapstructure:"api_key"`
	} `mapstructure:"mistral"`
	SMTP struct {
		Host        string `mapstructure:"host"`
		Transport   string `mapstructure:"transport"`
		Username    string `mapstructure:"username"`
		Password    string `mapstructure:"password"`
		SenderEmail string `mapstructure:"sender_email"`
		SenderName  string `mapstructure:"sender_name"`
		Port        int    `mapstructure:"port"`
	} `mapstructure:"smtp"`
	ClickSend struct {
		Username string `mapstructure:"username"`
		APIKey   string `mapstructure:"api_key"`
		SenderID string `mapstructure:"sender_id"`
		BaseURL  string `mapstructure:"base_url"`
	} `mapstructure:"clicksend"`
	ElevenLabs struct {
		APIKey             string `mapstructure:"api_key"`
		CacheRetentionDays int    `mapstructure:"cache_retention_days"`
	} `mapstructure:"eleven_labs"`
	Uploads struct {
		MaxFileSizeMiB     int `mapstructure:"max_file_size_mib"`
		MaxMaterialSizeMiB int `mapstructure:"max_material_size_mib"`
		MaxMaterialPages   int `mapstructure:"max_material_pages"`
		MaxMaterialFiles   int `mapstructure:"max_material_files"`
		MaxImageMegapixels int `mapstructure:"max_image_megapixels"`
	} `mapstructure:"uploads"`
}

// AddFlags adds all non-secret command-line configuration overrides.
func AddFlags(flags *pflag.FlagSet) {
	flags.String("config", "/etc/mia/mia.toml", "configuration file")
	for _, item := range settings {
		if item.secret {
			continue
		}
		switch item.kind {
		case valueString:
			flags.String(item.flag, "", "")
		case valueInt:
			flags.Int(item.flag, 0, "")
		case valueStrings:
			flags.String(item.flag, "", "")
		}
	}
}

// Load reads the configured sources with defaults < TOML < environment < flags.
func Load(flags *pflag.FlagSet, serve bool) (Config, error) {
	v := viper.New()
	v.SetEnvPrefix("MIA")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AllowEmptyEnv(true)
	for _, item := range settings {
		v.SetDefault(item.key, item.defaultValue)
		if err := v.BindEnv(item.key); err != nil {
			return Config{}, fmt.Errorf("bind environment: %w", err)
		}
		if !item.secret {
			if err := v.BindPFlag(item.key, flags.Lookup(item.flag)); err != nil {
				return Config{}, fmt.Errorf("bind flag: %w", err)
			}
		}
	}
	configPath, err := flags.GetString("config")
	if err != nil {
		return Config{}, fmt.Errorf("read config flag: %w", err)
	}
	explicit := flags.Changed("config")
	if err := readFile(v, configPath, explicit); err != nil {
		return Config{}, err
	}
	var result Config
	if err := v.UnmarshalExact(&result); err != nil {
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}
	if err := normalizeListSettings(&result); err != nil {
		return Config{}, err
	}
	if err := validate(&result, serve, optionalProviderPresence(v, flags)); err != nil {
		return Config{}, err
	}
	return result, nil
}

func normalizeListSettings(config *Config) error {
	var values []string
	for _, raw := range config.HTTP.TrustedProxyCIDRs {
		for _, value := range strings.Split(raw, ",") {
			value = strings.TrimSpace(value)
			if value == "" {
				return errors.New("http.trusted_proxy_cidrs contains an empty value")
			}
			values = append(values, value)
		}
	}
	config.HTTP.TrustedProxyCIDRs = values
	return nil
}

func readFile(v *viper.Viper, path string, explicit bool) error {
	if err := validateConfigFile(path); err != nil {
		if errors.Is(err, os.ErrNotExist) && !explicit {
			return nil
		}
		return err
	}
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("read configuration file: %w", err)
	}
	return nil
}

func validateConfigFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("configuration file is not regular")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 && stat.Uid != uint32(os.Geteuid()) {
		return errors.New("configuration file has an untrusted owner")
	}
	if info.Mode().Perm()&0o137 != 0 {
		return errors.New("configuration file has unsafe permissions")
	}
	return nil
}

type optionalProviders struct{ clickSend, elevenLabs bool }

func optionalProviderPresence(v *viper.Viper, flags *pflag.FlagSet) optionalProviders {
	present := func(table string, keys ...string) bool {
		if v.InConfig(table) {
			return true
		}
		for _, key := range keys {
			settingKey := table + "." + key
			if _, configured := os.LookupEnv(environmentName(settingKey)); v.InConfig(settingKey) || configured {
				return true
			}
			for _, item := range settings {
				if item.key == settingKey && !item.secret && flags.Changed(item.flag) {
					return true
				}
			}
		}
		return false
	}
	return optionalProviders{clickSend: present("clicksend", "username", "api_key", "sender_id", "base_url"), elevenLabs: present("eleven_labs", "api_key", "cache_retention_days")}
}

// environmentName returns the environment variable that overrides a setting.
func environmentName(settingKey string) string {
	return "MIA_" + strings.ToUpper(strings.ReplaceAll(settingKey, ".", "_"))
}

// requiredSettings reports every mandatory setting that is still empty, so one
// startup attempt names all of them instead of revealing them one at a time.
func requiredSettings(config *Config) error {
	var missing []string
	for _, required := range []struct{ key, value string }{
		{"openai.api_key", config.OpenAI.APIKey},
		{"mistral.api_key", config.Mistral.APIKey},
		{"smtp.host", config.SMTP.Host},
		{"smtp.sender_email", config.SMTP.SenderEmail},
	} {
		if required.value == "" {
			missing = append(missing, required.key+" ("+environmentName(required.key)+")")
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("required configuration is missing: %s", strings.Join(missing, ", "))
	}
	return nil
}

// uploadLimits validates each configurable bound separately so the operator
// learns which limit is wrong and what range it accepts.
func uploadLimits(config *Config) error {
	for _, limit := range []struct {
		key              string
		value, low, high int
	}{
		{"uploads.max_file_size_mib", config.Uploads.MaxFileSizeMiB, 1, 512},
		{"uploads.max_material_size_mib", config.Uploads.MaxMaterialSizeMiB, 1, 512},
		{"uploads.max_material_pages", config.Uploads.MaxMaterialPages, 1, 2000},
		{"uploads.max_material_files", config.Uploads.MaxMaterialFiles, 1, 200},
		{"uploads.max_image_megapixels", config.Uploads.MaxImageMegapixels, 1, 100},
	} {
		if limit.value < limit.low || limit.value > limit.high {
			return fmt.Errorf("%s must be between %d and %d", limit.key, limit.low, limit.high)
		}
	}
	if config.Uploads.MaxMaterialSizeMiB < config.Uploads.MaxFileSizeMiB {
		return errors.New("uploads.max_material_size_mib must be greater than or equal to uploads.max_file_size_mib")
	}
	return nil
}

func validate(config *Config, serve bool, optional optionalProviders) error {
	if err := requiredPath("main.data_dir", config.Main.DataDir, true); err != nil {
		return err
	}
	if !serve {
		return nil
	}
	if err := normalizeClickSendBaseURL(config); err != nil {
		return err
	}
	if err := requiredPath("main.doc_root", config.Main.DocRoot, false); err != nil {
		return err
	}
	data, err := filepath.EvalSymlinks(config.Main.DataDir)
	if err != nil {
		return fmt.Errorf("resolve main.data_dir: %w", err)
	}
	root, err := filepath.EvalSymlinks(config.Main.DocRoot)
	if err != nil {
		return fmt.Errorf("resolve main.doc_root: %w", err)
	}
	if data == root || strings.HasPrefix(data, root+string(os.PathSeparator)) || strings.HasPrefix(root, data+string(os.PathSeparator)) {
		return errors.New("main.data_dir and main.doc_root must be disjoint")
	}
	if err := normalizePublicURL(config); err != nil {
		return err
	}
	if err := listen(config.HTTP.Listen); err != nil {
		return err
	}
	if err := cookiePolicy(config); err != nil {
		return err
	}
	if strings.HasPrefix(config.HTTP.Listen, "unix:") && config.HTTP.SocketGroup == "" { /* primary group is used */
	} else if !strings.HasPrefix(config.HTTP.Listen, "unix:") && config.HTTP.SocketGroup != "" {
		return errors.New("http.socket_group requires a Unix listener")
	}
	for _, cidr := range config.HTTP.TrustedProxyCIDRs {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			return fmt.Errorf("invalid trusted proxy CIDR: %w", err)
		}
	}
	if config.Log.Level != "debug" && config.Log.Level != "info" && config.Log.Level != "warn" && config.Log.Level != "error" {
		return errors.New("invalid log.level")
	}
	if config.Log.Format != "json" && config.Log.Format != "text" {
		return errors.New("invalid log.format")
	}
	if config.Log.File != "" && !filepath.IsAbs(config.Log.File) {
		return errors.New("log.file must be absolute")
	}
	if err := requiredSettings(config); err != nil {
		return err
	}
	if !supportedModel(config.OpenAI.ChatModel) {
		return fmt.Errorf("openai.chat_model %q lacks a supported tokenizer and capability mapping", config.OpenAI.ChatModel)
	}
	if !supportedModel(config.OpenAI.JobModel) {
		return fmt.Errorf("openai.job_model %q lacks a supported tokenizer and capability mapping", config.OpenAI.JobModel)
	}
	if _, _, err := identity.Email(config.SMTP.SenderEmail); err != nil {
		return errors.New("invalid smtp.sender_email")
	}
	if config.SMTP.Port < 1 || config.SMTP.Port > 65535 {
		return errors.New("invalid smtp.port")
	}
	if config.SMTP.Transport != "starttls" && config.SMTP.Transport != "implicit_tls" && config.SMTP.Transport != "plaintext" {
		return errors.New("invalid smtp.transport")
	}
	if (config.SMTP.Username == "") != (config.SMTP.Password == "") {
		return errors.New("smtp.username and smtp.password must be configured together")
	}
	if optional.clickSend && (config.ClickSend.Username == "" || config.ClickSend.APIKey == "") {
		return errors.New("clicksend.username and clicksend.api_key must be configured together")
	}
	if optional.elevenLabs && config.ElevenLabs.APIKey == "" {
		return errors.New("eleven_labs.api_key is required when ElevenLabs is configured")
	}
	if optional.elevenLabs && (config.ElevenLabs.CacheRetentionDays < 1 || config.ElevenLabs.CacheRetentionDays > 365) {
		return errors.New("invalid eleven_labs.cache_retention_days")
	}
	return uploadLimits(config)
}

func requiredPath(name, value string, private bool) error {
	if value == "" {
		return fmt.Errorf("%s must be configured", name)
	}
	if !filepath.IsAbs(value) {
		return fmt.Errorf("%s must be an absolute path", name)
	}
	info, err := os.Stat(value)
	if err != nil {
		return fmt.Errorf("stat %s: %w", name, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", name)
	}
	if !private {
		directory, err := os.Open(value)
		if err != nil {
			return fmt.Errorf("open %s: %w", name, err)
		}
		_, readErr := directory.Readdirnames(1)
		if closeErr := directory.Close(); closeErr != nil {
			return fmt.Errorf("close %s: %w", name, closeErr)
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return fmt.Errorf("read %s: %w", name, readErr)
		}
	}
	if private && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s has unsafe permissions", name)
	}
	if private {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(os.Geteuid()) {
			return fmt.Errorf("%s is not owned by the service user", name)
		}
	}
	return nil
}

// supportedModels is the local capability and tokenizer registry. Provider
// availability is deliberately not checked during startup.
var supportedModels = map[string]struct{}{"gpt-5.6-terra": {}}

func supportedModel(value string) bool { _, ok := supportedModels[value]; return ok }

// normalizePublicURL validates the origin MIA embeds into invitation and
// password-recovery links. HTTP is restricted to loopback hosts so local
// development and tests work without TLS while no deployable origin can emit
// unencrypted links.
func normalizePublicURL(config *Config) error {
	parsed, err := url.Parse(config.Main.PublicURL)
	if err != nil {
		return fmt.Errorf("invalid main.public_url: %w", err)
	}
	if parsed.Host == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("main.public_url must be an HTTPS origin or a loopback HTTP origin")
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		if !loopbackHost(parsed.Hostname()) {
			return errors.New("main.public_url HTTP requires localhost or a loopback IP")
		}
	default:
		return errors.New("main.public_url must use HTTPS or loopback HTTP")
	}
	parsed.Path = ""
	config.Main.PublicURL = parsed.String()
	return nil
}

// cookiePolicy derives browser cookie settings from the validated public origin.
// HTTP is usable only on a loopback TCP listener, so non-secure cookies cannot
// become reachable through a proxy, wildcard, or remote listener.
func cookiePolicy(config *Config) error {
	if strings.HasPrefix(config.Main.PublicURL, "http://") {
		if !loopbackTCPListener(config.HTTP.Listen) {
			return errors.New("loopback HTTP main.public_url requires a loopback TCP http.listen")
		}
		if len(config.HTTP.TrustedProxyCIDRs) != 0 {
			return errors.New("loopback HTTP main.public_url does not allow trusted proxies")
		}
		config.CookiePolicy = httpserver.CookiePolicy{SessionName: "mia_session", CSRFName: "mia_csrf"}
		return nil
	}
	config.CookiePolicy = httpserver.CookiePolicy{SessionName: "__Host-mia_session", CSRFName: "__Host-mia_csrf", Secure: true}
	return nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

// normalizeClickSendBaseURL validates the only provider endpoint base MIA supports.
// HTTP is restricted to local test doubles so operator configuration cannot send SMS
// credentials or message content to an arbitrary unencrypted host.
func normalizeClickSendBaseURL(config *Config) error {
	parsed, err := url.Parse(config.ClickSend.BaseURL)
	if err != nil {
		return fmt.Errorf("invalid clicksend.base_url: %w", err)
	}
	if parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" {
		return errors.New("clicksend.base_url must be an HTTPS origin or a loopback HTTP endpoint")
	}
	if strings.HasSuffix(parsed.Host, ":") {
		return errors.New("clicksend.base_url port must be between 1 and 65535")
	}
	if parsed.Port() != "" {
		port, err := strconv.Atoi(parsed.Port())
		if err != nil || port < 1 || port > 65535 {
			return errors.New("clicksend.base_url port must be between 1 and 65535")
		}
	}
	if parsed.Path != "" && parsed.Path != "/" && parsed.Path != "/v3" && parsed.Path != "/v3/" {
		return errors.New("clicksend.base_url must not contain an unsupported path")
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		if !loopbackHost(parsed.Hostname()) {
			return errors.New("clicksend.base_url HTTP requires localhost or a loopback IP")
		}
	default:
		return errors.New("clicksend.base_url must use HTTPS or loopback HTTP")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	config.ClickSend.BaseURL = parsed.String()
	return nil
}

func listen(value string) error {
	if strings.HasPrefix(value, "unix:") {
		if !filepath.IsAbs(strings.TrimPrefix(value, "unix:")) {
			return errors.New("unix listener path must be absolute")
		}
		return nil
	}
	if _, _, err := net.SplitHostPort(value); err != nil {
		return fmt.Errorf("invalid http.listen: %w", err)
	}
	return nil
}

func loopbackTCPListener(value string) bool {
	if strings.HasPrefix(value, "unix:") {
		return false
	}
	host, _, err := net.SplitHostPort(value)
	return err == nil && loopbackHost(host)
}

type valueKind uint8

const (
	valueString valueKind = iota
	valueInt
	valueStrings
)

type setting struct {
	key, flag    string
	defaultValue any
	secret       bool
	kind         valueKind
}

var settings = []setting{
	{"main.data_dir", "main-data-dir", "", false, valueString}, {"main.doc_root", "main-doc-root", "", false, valueString}, {"main.public_url", "main-public-url", "", false, valueString},
	{"http.listen", "http-listen", "127.0.0.1:9900", false, valueString}, {"http.trusted_proxy_cidrs", "http-trusted-proxy-cidrs", []string{}, false, valueStrings}, {"http.socket_group", "http-socket-group", "", false, valueString},
	{"log.file", "log-file", "", false, valueString}, {"log.level", "log-level", "info", false, valueString}, {"log.format", "log-format", "json", false, valueString},
	{"openai.api_key", "", "", true, valueString}, {"openai.chat_model", "openai-chat-model", "gpt-5.6-terra", false, valueString}, {"openai.job_model", "openai-job-model", "gpt-5.6-terra", false, valueString}, {"mistral.api_key", "", "", true, valueString},
	{"smtp.host", "smtp-host", "", false, valueString}, {"smtp.port", "smtp-port", 587, false, valueInt}, {"smtp.transport", "smtp-transport", "starttls", false, valueString}, {"smtp.username", "", "", true, valueString}, {"smtp.password", "", "", true, valueString}, {"smtp.sender_email", "smtp-sender-email", "", false, valueString}, {"smtp.sender_name", "smtp-sender-name", "", false, valueString},
	{"clicksend.username", "", "", true, valueString}, {"clicksend.api_key", "", "", true, valueString}, {"clicksend.sender_id", "clicksend-sender-id", "", false, valueString}, {"clicksend.base_url", "clicksend-base-url", "https://rest.clicksend.com/v3", false, valueString}, {"eleven_labs.api_key", "", "", true, valueString}, {"eleven_labs.cache_retention_days", "eleven-labs-cache-retention-days", 30, false, valueInt},
	{"uploads.max_file_size_mib", "uploads-max-file-size-mib", 100, false, valueInt}, {"uploads.max_material_size_mib", "uploads-max-material-size-mib", 200, false, valueInt}, {"uploads.max_material_pages", "uploads-max-material-pages", 1000, false, valueInt}, {"uploads.max_material_files", "uploads-max-material-files", 200, false, valueInt}, {"uploads.max_image_megapixels", "uploads-max-image-megapixels", 40, false, valueInt},
}

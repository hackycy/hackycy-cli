package server

import (
	"context"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/hackycy/hackycy-cli/internal/logging"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

const serverMaximumSafeInteger = int64(9007199254740991)

// ServerOptionInput retains whether a future CLI flag was set. A nil field
// defers to the matching environment variable, while a non-nil empty string
// remains an explicit command-line value.
type ServerOptionInput struct {
	Address          *string
	ControlPort      *string
	FRPPort          *string
	HTTPPort         *string
	PortRange        *string
	AdvertiseFRPAddr *string
	DataDir          *string
	SessionIdleDays  *string
}

// ServerConfig is the resolved, private configuration for one Tunnel server
// process. It contains no browser-projected secrets.
type ServerConfig struct {
	Settings            ServerHTTPServerSettings
	AdminPassword       string
	SessionIdleLifetime time.Duration
	FRPToken            string
}

// ServerEnvironment distinguishes an absent environment variable from a
// deliberately configured empty string.
type ServerEnvironment func(string) (string, bool)

// ResolveServerConfig applies the retained tunnel-server CLI, environment,
// and default precedence without registering the public command.
func ResolveServerConfig(input ServerOptionInput, environment ServerEnvironment) (ServerConfig, error) {
	return resolveServerConfig(input, environment, defaultServerDataDirectory)
}

func resolveServerConfig(input ServerOptionInput, environment ServerEnvironment, defaultDataDirectory func() (string, error)) (ServerConfig, error) {
	if environment == nil {
		environment = os.LookupEnv
	}
	if defaultDataDirectory == nil {
		defaultDataDirectory = defaultServerDataDirectory
	}

	address := strings.TrimSpace(serverOptionValue(input.Address, environment, "YCY_TUNNEL_ADDRESS", "0.0.0.0"))
	if address == "" {
		return ServerConfig{}, fmt.Errorf("Tunnel server address is required")
	}
	controlPort, err := parseServerPort(serverOptionValue(input.ControlPort, environment, "YCY_TUNNEL_CONTROL_PORT", "7500"), "Control port")
	if err != nil {
		return ServerConfig{}, err
	}
	frpPort, err := parseServerPort(serverOptionValue(input.FRPPort, environment, "YCY_TUNNEL_FRP_PORT", "7000"), "FRP bind port")
	if err != nil {
		return ServerConfig{}, err
	}
	httpPort, err := parseServerPort(serverOptionValue(input.HTTPPort, environment, "YCY_TUNNEL_HTTP_PORT", "8080"), "FRP HTTP port")
	if err != nil {
		return ServerConfig{}, err
	}
	portRange, err := parseServerPortRange(serverOptionValue(input.PortRange, environment, "YCY_TUNNEL_PORT_RANGE", "20000-20100"))
	if err != nil {
		return ServerConfig{}, err
	}

	dataDirectory := serverOptionValue(input.DataDir, environment, "YCY_TUNNEL_DATA_DIR", "")
	if input.DataDir == nil {
		if _, configured := environment("YCY_TUNNEL_DATA_DIR"); !configured {
			dataDirectory, err = defaultDataDirectory()
			if err != nil {
				return ServerConfig{}, err
			}
		}
	}
	dataDirectory, err = filepath.Abs(dataDirectory)
	if err != nil {
		return ServerConfig{}, fmt.Errorf("resolve Tunnel server data directory: %w", err)
	}

	var advertised *ServerHTTPFRPAddress
	if value := strings.TrimSpace(serverOptionValue(input.AdvertiseFRPAddr, environment, "YCY_TUNNEL_ADVERTISE_FRP_ADDR", "")); value != "" {
		parsed, parseErr := parseServerFRPAddress(value)
		if parseErr != nil {
			return ServerConfig{}, parseErr
		}
		advertised = &parsed
	}

	adminUsername := "admin"
	if configured, ok := environment("YCY_TUNNEL_ADMIN_USER"); ok {
		adminUsername = configured
	}
	if !validServerAdministratorUsername(adminUsername) {
		return ServerConfig{}, fmt.Errorf("Environment administrator username must contain 1-64 ASCII letters, numbers, dots, underscores, or hyphens")
	}
	adminPassword, configuredPassword := environment("YCY_TUNNEL_ADMIN_PASSWORD")
	if !configuredPassword || !validServerAdministratorPassword(adminPassword) {
		return ServerConfig{}, fmt.Errorf("YCY_TUNNEL_ADMIN_PASSWORD must contain 5-256 characters")
	}

	frpToken := ""
	if configured, ok := environment("YCY_TUNNEL_FRP_TOKEN"); ok {
		frpToken = strings.TrimSpace(configured)
		if frpToken == "" {
			return ServerConfig{}, fmt.Errorf("FRP Token must not be empty")
		}
	}

	sessionIdleDays, err := parseServerPositiveSafeInteger(serverOptionValue(input.SessionIdleDays, environment, "YCY_TUNNEL_SESSION_IDLE_DAYS", "7"))
	if err != nil {
		return ServerConfig{}, fmt.Errorf("Session idle lifetime must be a positive integer number of days")
	}
	listenerPorts := []int{controlPort, frpPort, httpPort}
	if listenerPorts[0] == listenerPorts[1] || listenerPorts[0] == listenerPorts[2] || listenerPorts[1] == listenerPorts[2] {
		return ServerConfig{}, fmt.Errorf("Control, FRP bind, and FRP HTTP listener ports must be distinct")
	}
	for _, listenerPort := range listenerPorts {
		if listenerPort >= portRange.Start && listenerPort <= portRange.End {
			return ServerConfig{}, fmt.Errorf("Server Port Pool must not include listener port %d", listenerPort)
		}
	}

	return ServerConfig{
		Settings: ServerHTTPServerSettings{
			Address:          address,
			ControlPort:      controlPort,
			FRPPort:          frpPort,
			HTTPPort:         httpPort,
			PortRange:        portRange,
			AdvertiseFRPAddr: advertised,
			DataDir:          dataDirectory,
			AdminUser:        adminUsername,
		},
		AdminPassword:       adminPassword,
		SessionIdleLifetime: time.Duration(sessionIdleDays) * 24 * time.Hour,
		FRPToken:            frpToken,
	}, nil
}

func serverOptionValue(value *string, environment ServerEnvironment, name, fallback string) string {
	if value != nil {
		return *value
	}
	if configured, ok := environment(name); ok {
		return configured
	}
	return fallback
}

func parseServerPort(value, label string) (int, error) {
	parsed, err := parseServerPositiveSafeInteger(value)
	if err != nil || parsed > 65535 {
		return 0, fmt.Errorf("%s must be an integer from 1 through 65535", label)
	}
	return int(parsed), nil
}

func parseServerPositiveSafeInteger(value string) (int64, error) {
	number, err := parseServerNumber(value)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || number < 1 || number > float64(serverMaximumSafeInteger) {
		return 0, fmt.Errorf("not a positive safe integer")
	}
	return int64(number), nil
}

// parseServerNumber intentionally accepts the JavaScript Number spellings
// exercised by the legacy option parser, including hexadecimal, binary, and
// octal integer literals. The caller imposes the positive-safe-integer rule.
func parseServerNumber(value string) (float64, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "_") {
		return 0, fmt.Errorf("not a number")
	}
	if len(value) > 2 && value[0] == '0' {
		base := 0
		switch value[1] {
		case 'x', 'X':
			base = 16
		case 'b', 'B':
			base = 2
		case 'o', 'O':
			base = 8
		}
		if base != 0 {
			parsed, err := strconv.ParseUint(value[2:], base, 64)
			if err != nil {
				return 0, err
			}
			return float64(parsed), nil
		}
	}
	return strconv.ParseFloat(value, 64)
}

func parseServerPortRange(value string) (ServerHTTPPortRange, error) {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) != 2 || !serverDecimalDigits(parts[0]) || !serverDecimalDigits(parts[1]) {
		return ServerHTTPPortRange{}, fmt.Errorf("Server Port Pool must use start-end syntax")
	}
	start, err := parseServerPort(parts[0], "Server Port Pool start")
	if err != nil {
		return ServerHTTPPortRange{}, err
	}
	end, err := parseServerPort(parts[1], "Server Port Pool end")
	if err != nil {
		return ServerHTTPPortRange{}, err
	}
	if start > end {
		return ServerHTTPPortRange{}, fmt.Errorf("Server Port Pool start must not exceed its end")
	}
	return ServerHTTPPortRange{Start: start, End: end}, nil
}

func serverDecimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func parseServerFRPAddress(value string) (ServerHTTPFRPAddress, error) {
	host := ""
	rawPort := ""
	if strings.HasPrefix(value, "[") {
		separator := strings.Index(value, "]:")
		if separator > 1 {
			host = value[1:separator]
			rawPort = value[separator+2:]
		}
		ip := net.ParseIP(host)
		if ip == nil || !strings.Contains(host, ":") {
			return ServerHTTPFRPAddress{}, fmt.Errorf("Advertised FRP address must be host:port or [IPv6]:port")
		}
	} else {
		separator := strings.LastIndex(value, ":")
		if separator <= 0 || strings.Index(value, ":") != separator {
			return ServerHTTPFRPAddress{}, fmt.Errorf("Advertised FRP address must be host:port or [IPv6]:port")
		}
		host = strings.TrimSpace(value[:separator])
		rawPort = value[separator+1:]
	}
	if host == "" || strings.ContainsAny(host, "/?#@") {
		return ServerHTTPFRPAddress{}, fmt.Errorf("Advertised FRP host is invalid")
	}
	port, err := parseServerPort(rawPort, "Advertised FRP port")
	if err != nil {
		return ServerHTTPFRPAddress{}, err
	}
	return ServerHTTPFRPAddress{Host: host, Port: port}, nil
}

func validServerAdministratorUsername(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '.' && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func validServerAdministratorPassword(value string) bool {
	length := len(utf16.Encode([]rune(value)))
	return length >= 5 && length <= 256
}

func defaultServerDataDirectory() (string, error) {
	return serverDataDirectory(os.Getenv, os.UserHomeDir, runtime.GOOS)
}

func serverDataDirectory(environment func(string) string, userHomeDirectory func() (string, error), platform string) (string, error) {
	stateRoot, err := tunnelruntime.StateRoot(environment, userHomeDirectory, platform)
	if err != nil {
		return "", err
	}
	return filepath.Join(stateRoot, "ycy", "tunnel", "server"), nil
}

// ServerRunOptions identifies the foreground ownership supplied by the
// command leaf. The private constructor hook keeps native lifecycle tests
// independent of network acquisition fixtures.
type ServerRunOptions struct {
	Logger logging.Logger

	newRuntime func(context.Context, ServerRuntimeOptions) (*ServerRuntime, error)
}

// RunServer owns one foreground Tunnel server lifecycle. Signal handling is
// supplied by the composition root through ctx; cancellation takes the same
// ordered close path as a normal server shutdown.
func RunServer(ctx context.Context, config ServerConfig, options ServerRunOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return nil
	}
	lifecycle := newServerLifecycle(options.Logger)
	lifecycle.starting(config)
	newRuntime := options.newRuntime
	if newRuntime == nil {
		newRuntime = NewServerRuntime
	}
	runtime, err := newRuntime(ctx, ServerRuntimeOptions{
		Settings:            config.Settings,
		AdminPassword:       config.AdminPassword,
		SessionIdleLifetime: config.SessionIdleLifetime,
		FRPToken:            config.FRPToken,
		FRPSLogger:          options.Logger,
		LifecycleLogger:     options.Logger,
	})
	if err != nil {
		if ctx.Err() == nil {
			lifecycle.failed("state.open_failed", err)
			lifecycle.stopped(ctx, err)
			return err
		}
		return nil
	}
	lifecycle.stateOpened(config)
	stopFRPSObserver := runtime.supervisor.Observe(lifecycle.frpsState)
	defer stopFRPSObserver()
	if ctx.Err() != nil {
		closeErr := runtime.Close()
		lifecycle.shutdown(ctx, "cancelled")
		if closeErr != nil {
			lifecycle.failed("shutdown.cleanup_failed", closeErr)
		}
		lifecycle.stopped(ctx, closeErr)
		return closeErr
	}
	server, err := runtime.start(false)
	if err != nil {
		if ctx.Err() == nil {
			lifecycle.failed("control.bind_failed", err)
			lifecycle.stopped(ctx, err)
			return err
		}
		return nil
	}
	lifecycle.listening(server.Port())
	lifecycle.started()
	lifecycle.frpsPreparing()
	server.startManagedFRPS()

	waited := make(chan error, 1)
	go func() {
		waited <- server.Wait()
	}()
	select {
	case err := <-waited:
		if err != nil {
			lifecycle.failed("control.listener_failed", err)
			lifecycle.stopped(ctx, err)
			return err
		}
		lifecycle.stopped(ctx, nil)
		return nil
	case <-ctx.Done():
		lifecycle.shutdown(ctx, "cancelled")
		if closeErr := server.Close(); closeErr != nil {
			lifecycle.failed("shutdown.cleanup_failed", closeErr)
			lifecycle.stopped(ctx, closeErr)
			return closeErr
		}
		lifecycle.stopped(ctx, nil)
		return nil
	}
}

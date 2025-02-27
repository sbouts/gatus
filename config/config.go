package config

import (
	"errors"
	"io/ioutil"
	"log"
	"os"
	"time"

	"github.com/TwinProduction/gatus/alerting"
	"github.com/TwinProduction/gatus/alerting/alert"
	"github.com/TwinProduction/gatus/alerting/provider"
	"github.com/TwinProduction/gatus/config/maintenance"
	"github.com/TwinProduction/gatus/config/ui"
	"github.com/TwinProduction/gatus/config/web"
	"github.com/TwinProduction/gatus/core"
	"github.com/TwinProduction/gatus/security"
	"github.com/TwinProduction/gatus/storage"
	"gopkg.in/yaml.v2"
)

const (
	// DefaultConfigurationFilePath is the default path that will be used to search for the configuration file
	// if a custom path isn't configured through the GATUS_CONFIG_FILE environment variable
	DefaultConfigurationFilePath = "config/config.yaml"

	// DefaultFallbackConfigurationFilePath is the default fallback path that will be used to search for the
	// configuration file if DefaultConfigurationFilePath didn't work
	DefaultFallbackConfigurationFilePath = "config/config.yml"
)

var (
	// ErrNoServiceInConfig is an error returned when a configuration file has no services configured
	ErrNoServiceInConfig = errors.New("configuration file should contain at least 1 service")

	// ErrConfigFileNotFound is an error returned when the configuration file could not be found
	ErrConfigFileNotFound = errors.New("configuration file not found")

	// ErrInvalidSecurityConfig is an error returned when the security configuration is invalid
	ErrInvalidSecurityConfig = errors.New("invalid security configuration")
)

// Config is the main configuration structure
type Config struct {
	// Debug Whether to enable debug logs
	Debug bool `yaml:"debug"`

	// Metrics Whether to expose metrics at /metrics
	Metrics bool `yaml:"metrics"`

	// SkipInvalidConfigUpdate Whether to make the application ignore invalid configuration
	// if the configuration file is updated while the application is running
	SkipInvalidConfigUpdate bool `yaml:"skip-invalid-config-update"`

	// DisableMonitoringLock Whether to disable the monitoring lock
	// The monitoring lock is what prevents multiple services from being processed at the same time.
	// Disabling this may lead to inaccurate response times
	DisableMonitoringLock bool `yaml:"disable-monitoring-lock"`

	// Security Configuration for securing access to Gatus
	Security *security.Config `yaml:"security"`

	// Alerting Configuration for alerting
	Alerting *alerting.Config `yaml:"alerting"`

	// Services List of services to monitor
	Services []*core.Service `yaml:"services"`

	// Storage is the configuration for how the data is stored
	Storage *storage.Config `yaml:"storage"`

	// Web is the configuration for the web listener
	Web *web.Config `yaml:"web"`

	// UI is the configuration for the UI
	UI *ui.Config `yaml:"ui"`

	// Maintenance is the configuration for creating a maintenance window in which no alerts are sent
	Maintenance *maintenance.Config `yaml:"maintenance"`

	filePath        string    // path to the file from which config was loaded from
	lastFileModTime time.Time // last modification time
}

// HasLoadedConfigurationFileBeenModified returns whether the file that the
// configuration has been loaded from has been modified since it was last read
func (config Config) HasLoadedConfigurationFileBeenModified() bool {
	if fileInfo, err := os.Stat(config.filePath); err == nil {
		if !fileInfo.ModTime().IsZero() {
			return config.lastFileModTime.Unix() != fileInfo.ModTime().Unix()
		}
	}
	return false
}

// UpdateLastFileModTime refreshes Config.lastFileModTime
func (config *Config) UpdateLastFileModTime() {
	if fileInfo, err := os.Stat(config.filePath); err == nil {
		if !fileInfo.ModTime().IsZero() {
			config.lastFileModTime = fileInfo.ModTime()
		}
	} else {
		log.Println("[config][UpdateLastFileModTime] Ran into error updating lastFileModTime:", err.Error())
	}
}

// Load loads a custom configuration file
// Note that the misconfiguration of some fields may lead to panics. This is on purpose.
func Load(configFile string) (*Config, error) {
	log.Printf("[config][Load] Reading configuration from configFile=%s", configFile)
	cfg, err := readConfigurationFile(configFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrConfigFileNotFound
		}
		return nil, err
	}
	cfg.filePath = configFile
	cfg.UpdateLastFileModTime()
	return cfg, nil
}

// LoadDefaultConfiguration loads the default configuration file
func LoadDefaultConfiguration() (*Config, error) {
	cfg, err := Load(DefaultConfigurationFilePath)
	if err != nil {
		if err == ErrConfigFileNotFound {
			return Load(DefaultFallbackConfigurationFilePath)
		}
		return nil, err
	}
	return cfg, nil
}

func readConfigurationFile(fileName string) (config *Config, err error) {
	var bytes []byte
	if bytes, err = ioutil.ReadFile(fileName); err == nil {
		// file exists, so we'll parse it and return it
		return parseAndValidateConfigBytes(bytes)
	}
	return
}

func parseAndValidateConfigBytes(yamlBytes []byte) (config *Config, err error) {
	// Expand environment variables
	yamlBytes = []byte(os.ExpandEnv(string(yamlBytes)))
	// Parse configuration file
	err = yaml.Unmarshal(yamlBytes, &config)
	if err != nil {
		return
	}
	// Check if the configuration file at least has services configured
	if config == nil || config.Services == nil || len(config.Services) == 0 {
		err = ErrNoServiceInConfig
	} else {
		// Note that the functions below may panic, and this is on purpose to prevent Gatus from starting with
		// invalid configurations
		validateAlertingConfig(config.Alerting, config.Services, config.Debug)
		if err := validateSecurityConfig(config); err != nil {
			return nil, err
		}
		if err := validateServicesConfig(config); err != nil {
			return nil, err
		}
		if err := validateWebConfig(config); err != nil {
			return nil, err
		}
		if err := validateUIConfig(config); err != nil {
			return nil, err
		}
		if err := validateMaintenanceConfig(config); err != nil {
			return nil, err
		}
		if err := validateStorageConfig(config); err != nil {
			return nil, err
		}
	}
	return
}

func validateStorageConfig(config *Config) error {
	if config.Storage == nil {
		config.Storage = &storage.Config{
			Type: storage.TypeMemory,
		}
	}
	err := storage.Initialize(config.Storage)
	if err != nil {
		return err
	}
	// Remove all ServiceStatus that represent services which no longer exist in the configuration
	var keys []string
	for _, service := range config.Services {
		keys = append(keys, service.Key())
	}
	numberOfServiceStatusesDeleted := storage.Get().DeleteAllServiceStatusesNotInKeys(keys)
	if numberOfServiceStatusesDeleted > 0 {
		log.Printf("[config][validateStorageConfig] Deleted %d service statuses because their matching services no longer existed", numberOfServiceStatusesDeleted)
	}
	return nil
}

func validateMaintenanceConfig(config *Config) error {
	if config.Maintenance == nil {
		config.Maintenance = maintenance.GetDefaultConfig()
	} else {
		if err := config.Maintenance.ValidateAndSetDefaults(); err != nil {
			return err
		}
	}
	return nil
}

func validateUIConfig(config *Config) error {
	if config.UI == nil {
		config.UI = ui.GetDefaultConfig()
	} else {
		if err := config.UI.ValidateAndSetDefaults(); err != nil {
			return err
		}
	}
	return nil
}

func validateWebConfig(config *Config) error {
	if config.Web == nil {
		config.Web = web.GetDefaultConfig()
	} else {
		return config.Web.ValidateAndSetDefaults()
	}
	return nil
}

func validateServicesConfig(config *Config) error {
	for _, service := range config.Services {
		if config.Debug {
			log.Printf("[config][validateServicesConfig] Validating service '%s'", service.Name)
		}
		if err := service.ValidateAndSetDefaults(); err != nil {
			return err
		}
	}
	log.Printf("[config][validateServicesConfig] Validated %d services", len(config.Services))
	return nil
}

func validateSecurityConfig(config *Config) error {
	if config.Security != nil {
		if config.Security.IsValid() {
			if config.Debug {
				log.Printf("[config][validateSecurityConfig] Basic security configuration has been validated")
			}
		} else {
			// If there was an attempt to configure security, then it must mean that some confidential or private
			// data are exposed. As a result, we'll force a panic because it's better to be safe than sorry.
			return ErrInvalidSecurityConfig
		}
	}
	return nil
}

// validateAlertingConfig validates the alerting configuration
// Note that the alerting configuration has to be validated before the service configuration, because the default alert
// returned by provider.AlertProvider.GetDefaultAlert() must be parsed before core.Service.ValidateAndSetDefaults()
// sets the default alert values when none are set.
func validateAlertingConfig(alertingConfig *alerting.Config, services []*core.Service, debug bool) {
	if alertingConfig == nil {
		log.Printf("[config][validateAlertingConfig] Alerting is not configured")
		return
	}
	alertTypes := []alert.Type{
		alert.TypeCustom,
		alert.TypeDiscord,
		alert.TypeMattermost,
		alert.TypeMessagebird,
		alert.TypePagerDuty,
		alert.TypeSlack,
		alert.TypeTeams,
		alert.TypeTelegram,
		alert.TypeTwilio,
	}
	var validProviders, invalidProviders []alert.Type
	for _, alertType := range alertTypes {
		alertProvider := alertingConfig.GetAlertingProviderByAlertType(alertType)
		if alertProvider != nil {
			if alertProvider.IsValid() {
				// Parse alerts with the provider's default alert
				if alertProvider.GetDefaultAlert() != nil {
					for _, service := range services {
						for alertIndex, serviceAlert := range service.Alerts {
							if alertType == serviceAlert.Type {
								if debug {
									log.Printf("[config][validateAlertingConfig] Parsing alert %d with provider's default alert for provider=%s in service=%s", alertIndex, alertType, service.Name)
								}
								provider.ParseWithDefaultAlert(alertProvider.GetDefaultAlert(), serviceAlert)
							}
						}
					}
				}
				validProviders = append(validProviders, alertType)
			} else {
				log.Printf("[config][validateAlertingConfig] Ignoring provider=%s because configuration is invalid", alertType)
				invalidProviders = append(invalidProviders, alertType)
			}
		} else {
			invalidProviders = append(invalidProviders, alertType)
		}
	}
	log.Printf("[config][validateAlertingConfig] configuredProviders=%s; ignoredProviders=%s", validProviders, invalidProviders)
}

package main

import (
	"fmt"
	"log"
	"net/http"
	"net/url"

	//"strings"
	"time"

	"github.com/alphagov/paas-prometheus-exporter/events"
	"github.com/cloudfoundry-community/go-cfclient"

	// sonde_events "github.com/cloudfoundry/sonde-go/events"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/alecthomas/kingpin.v2"
	// "os"
)

var (
	apiEndpoint       = kingpin.Flag("api-endpoint", "API endpoint").Default("https://api.10.244.0.34.xip.io").OverrideDefaultFromEnvar("API_ENDPOINT").String()
	statsdEndpoint    = kingpin.Flag("statsd-endpoint", "Statsd endpoint").Default("10.244.11.2:8125").OverrideDefaultFromEnvar("STATSD_ENDPOINT").String()
	statsdPrefix      = kingpin.Flag("statsd-prefix", "Statsd prefix").Default("mycf.").OverrideDefaultFromEnvar("STATSD_PREFIX").String()
	username          = kingpin.Flag("username", "UAA username.").Default("").OverrideDefaultFromEnvar("USERNAME").String()
	password          = kingpin.Flag("password", "UAA password.").Default("").OverrideDefaultFromEnvar("PASSWORD").String()
	clientID          = kingpin.Flag("client-id", "UAA client ID.").Default("").OverrideDefaultFromEnvar("CLIENT_ID").String()
	clientSecret      = kingpin.Flag("client-secret", "UAA client secret.").Default("").OverrideDefaultFromEnvar("CLIENT_SECRET").String()
	skipSSLValidation = kingpin.Flag("skip-ssl-validation", "Please don't").Default("false").OverrideDefaultFromEnvar("SKIP_SSL_VALIDATION").Bool()
	debug             = kingpin.Flag("debug", "Enable debug mode. This disables forwarding to statsd and prometheus and prints to stdout").Default("false").OverrideDefaultFromEnvar("DEBUG").Bool()
	updateFrequency   = kingpin.Flag("update-frequency", "The time in seconds, that takes between each apps update call.").Default("300").OverrideDefaultFromEnvar("UPDATE_FREQUENCY").Int64()
	//metricTemplate     = kingpin.Flag("metric-template", "The template that will form a new metric namespace.").Default(senders.DefaultTemplate).OverrideDefaultFromEnvar("METRIC_TEMPLATE").String()
	metricWhitelist    = kingpin.Flag("metric-whitelist", "Comma separated metric name prefixes to enable.").Default("").OverrideDefaultFromEnvar("METRIC_WHITELIST").String()
	prometheusBindPort = kingpin.Flag("prometheus-bind-port", "The port to bind to for prometheus metrics.").Default("8080").OverrideDefaultFromEnvar("PORT").Int()
	enableStatsd       = kingpin.Flag("enable-statsd", "Enable the statsd sender.").Default("true").OverrideDefaultFromEnvar("ENABLE_STATSD").Bool()
	enablePrometheus   = kingpin.Flag("enable-prometheus", "Enable the prometheus sender.").Default("false").OverrideDefaultFromEnvar("ENABLE_PROMETHEUS").Bool()
	enableLoggregator  = kingpin.Flag("enable-loggregator", "Enable the Loggregator sender.").Default("false").OverrideDefaultFromEnvar("ENABLE_LOGGREGATOR").Bool()
	enableLocking      = kingpin.Flag("enable-locking", "Enable locking via Locket.").Default("false").OverrideDefaultFromEnvar("ENABLE_LOCKING").Bool()
	locketAddress      = kingpin.Flag("locket-address", "address:port of Locket server.").Default("127.0.0.1:8891").OverrideDefaultFromEnvar("LOCKET_API_LOCATION").String()
	locketCACert       = kingpin.Flag("locket-ca-cert", "File path to Locket CA certificate.").Default("").OverrideDefaultFromEnvar("LOCKET_CA_CERT").String()
	locketClientCert   = kingpin.Flag("locket-client-cert", "File path to Locket client certificate.").Default("").OverrideDefaultFromEnvar("LOCKET_CLIENT_CERT").String()
	locketClientKey    = kingpin.Flag("locket-client-key", "File path to Locket client key.").Default("").OverrideDefaultFromEnvar("LOCKET_CLIENT_KEY").String()
)

var appWatchers = make(map[string]*events.AppWatcher)

func createNewWatcher(config *cfclient.Config, app cfclient.App) {
	appWatcher := events.NewAppWatcher(config, app, prometheus.WrapRegistererWith(
		prometheus.Labels{"guid": app.Guid, "app": app.Name},
		prometheus.DefaultRegisterer,
	))
	appWatchers[app.Guid] = appWatcher
	go appWatcher.Run()
}

func checkForNewApps(cf *cfclient.Client, config *cfclient.Config) error {
	apps, err := cf.ListAppsByQuery(url.Values{})
	if err != nil {
		return err
	}

	running := map[string]bool{}

	for _, app := range apps {
		running[app.Guid] = true

		appWatcher, present := appWatchers[app.Guid]
		if present {
			if appWatcher.AppName() != app.Name {
				// Name changed, stop and restart
				appWatcher.Close()
				createNewWatcher(config, app)
			} else {
				// notify watcher that instances may have changed
				appWatcher.UpdateApp(app)
			}
		} else {
			// new app
			createNewWatcher(config, app)
		}
	}

	for appGuid, appWatcher := range appWatchers {
		if ok := running[appGuid]; !ok {
			appWatcher.Close()
			delete(appWatchers, appGuid)
		}
	}
	return nil
}

func main() {
	kingpin.Parse()

	config := &cfclient.Config{
		ApiAddress:        *apiEndpoint,
		SkipSslValidation: *skipSSLValidation,
		Username:          *username,
		Password:          *password,
		ClientID:          *clientID,
		ClientSecret:      *clientSecret,
	}

	cf, err := cfclient.NewClient(config)
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		for {
			log.Println("checking for new apps")
			err := checkForNewApps(cf, config)
			if err != nil {
				log.Fatal(err)
			}

			time.Sleep(time.Duration(*updateFrequency) * time.Second)
		}
	}()

	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *prometheusBindPort), nil))
}
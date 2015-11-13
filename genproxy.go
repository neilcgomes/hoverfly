package main

import (
	log "github.com/Sirupsen/logrus"
	"github.com/codegangsta/negroni"
	"github.com/elazarl/goproxy"
	"github.com/meatballhat/negroni-logrus"

	"flag"
	"fmt"
	"net/http"
	"os"
	"regexp"
)

const DefaultPort = ":8500"

func main() {
	// getting proxy configuration
	verbose := flag.Bool("v", false, "should every proxy request be logged to stdout")
	record := flag.Bool("record", false, "should proxy record")
	destination := flag.String("destination", "^.*:80$", "destination URI to catch")
	flag.Parse()

	// getting settings
	initSettings()

	// overriding default settings
	AppConfig.recordState = *record

	// getting default database
	port := os.Getenv("ProxyPort")
	if port == "" {
		port = DefaultPort
	} else {
		port = fmt.Sprintf(":%s", port)
	}

	// Output to stderr instead of stdout, could also be a file.
	log.SetOutput(os.Stderr)
	log.SetFormatter(&log.TextFormatter{})

	redisPool := getRedisPool()
	defer redisPool.Close()

	cache := Cache{pool: redisPool,
		prefix: AppConfig.cachePrefix}

	// getting connections
	d := DBClient{
		cache: cache,
		http:  &http.Client{},
	}

	// creating proxy
	proxy := goproxy.NewProxyHttpServer()

	proxy.OnRequest(goproxy.ReqHostMatches(regexp.MustCompile("^.*$"))).
		HandleConnect(goproxy.AlwaysMitm)

	// hijacking plain connections
	proxy.OnRequest(goproxy.ReqHostMatches(regexp.MustCompile(*destination))).DoFunc(
		func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {

			log.Info("connection found......")
			log.Info(fmt.Sprintf("Url path:  %s", r.URL.Path))

			return d.processRequest(r)
		})

	go d.startAdminInterface()

	proxy.Verbose = *verbose

	// proxy starting message
	log.WithFields(log.Fields{
		"RedisAddress": AppConfig.redisAddress,
		"Destination":  *destination,
		"ProxyPort":    port,
	}).Info("Proxy is starting...")

	log.Error(http.ListenAndServe(port, proxy))
}

// processRequest - processes incoming requests and based on proxy state (record/playback)
// returns HTTP response.
func (d *DBClient) processRequest(req *http.Request) (*http.Request, *http.Response) {

	if AppConfig.recordState {
		log.Info("*** RECORD ***")
		newResponse, err := d.recordRequest(req)
		if err != nil {
			// something bad happened, passing through
			return req, nil
		} else {
			// discarding original requests and returns supplied response
			return req, newResponse
		}

	} else {
		log.Info("*** PLAYBACK ***")
		newResponse := d.getResponse(req)
		return req, newResponse
	}
}

func (d *DBClient) startAdminInterface() {
	// starting admin interface
	mux := getBoneRouter(*d)
	n := negroni.Classic()
	n.Use(negronilogrus.NewMiddleware())
	n.UseHandler(mux)

	// admin interface starting message
	log.WithFields(log.Fields{
		"RedisAddress": AppConfig.redisAddress,
		"AdminPort":    AppConfig.adminInterface,
	}).Info("Admin interface is starting...")

	n.Run(AppConfig.adminInterface)
}

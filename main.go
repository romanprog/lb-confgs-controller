package main

import (
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gorilla/mux"
)

// Nginx lb state.
type ServerInfo struct {
	LastSeenTime         int64
	LastConfReceivedTime int64
	CurentConfVersion    int
	ReceivedConfVersion  int
	LastErr              error
}

// A part of project struct, descride domain.
type projectDomainData struct {
	Redirect bool
	SslType  int
}

// Project struct, project metadata for config generation.
type projectMetadata struct {
	Domains      map[string]*projectDomainData
	Storage      string
	Version      string
	CacheUrl     string
	SessionsUrl  string
	DbMasterUrl  string
	DbSlaveUrl   string
	LogUrl       string
	CorePath     string
	DevMode      string
	FrontendPath string
	RootPath     string
}

type projectsMetadataType map[string]*projectMetadata

// List of active nginx lbs (ip - state).
var serversState = make(map[string]*ServerInfo)
var serversStateOld = make(map[string]*ServerInfo)

// remove server from list after serverTimeout unseen seconds
var serverTimeout int64 = 120

var consulUrl = flag.String("consul.url",
	getEnv("CONSUL_URL", "127.0.0.1:8500"),
	"Consul endpoint in format 'host:port'")

var listenPort = flag.String("listen.port",
	getEnv("LISTEN_PORT", "8081"),
	"Listened port.")

var configVersionKey = flag.String("version.key",
	getEnv("VERSION_KEY", "system/config/version"),
	"Version key name in consul.")

var configsDir = flag.String("conf.dir",
	getEnv("CONFIGS_DIR", "/etc/nginx/conf.d"),
	"Directory with configs for lb nodes.")

var vHostsTemplateFile = flag.String("template.file",
	getEnv("TEMPLATE_FILE", "/conf/vhosts.conf.tmpl"),
	"Template file name.")

var configsPkgsDir = flag.String("conf.pkg.dir",
	getEnv("CONFIGS_PKG_DIR", "/opt/controller/conf_pkgs"),
	"Dir for configs pack archives.")

var vhostsSslTmpl = flag.String("vhosts.ssl.tmpl",
	getEnv("VHOSTS_SSL_TMPL", "/conf/vhost_ssl.tmpl"),
	"Template for ssl virtualhost section.")

var vhostsNonSslTmpl = flag.String("vhosts.non.ssl.tmpl",
	getEnv("VHOSTS_NON_SSL_TMPL", "/conf/vhost_non_ssl.tmpl"),
	"Template for non ssl virtualhost section.")
	
func main() {

	//	package main
	flag.Parse()
	testTmpl()
	return
	// Start registered servers list processing.
	go serverListProcessing()

	// Generate and update config every time after start.
	time.Sleep(time.Second * 3)
	version, err := incrConsulConfVersion()
	if err != nil {
		log.Fatalln(err.Error())
	}
	projectsMetadataJson, err := getConsulKvJson("clients")
	projectsMetadata := parseConsulProjectsData(projectsMetadataJson)
	err = genConfig(projectsMetadata)
	if err != nil {
		log.Fatalln(err.Error())
	}
	pkgConfigs(version)

	// Start http server.
	startListen()

}

// Processing list of registered lb nodes (update info).
func serverListProcessing() {
	// Ticker for pause.
	var tick = make(<-chan time.Time)
	tick = time.Tick(4 * time.Second)
	// serversStateOld - copy of serversState to get diff between checks (only for logs)
	for host, state := range serversState {
		serversStateOld[host] = &ServerInfo{0, 0, state.CurentConfVersion, state.ReceivedConfVersion, nil}
	}
	for {
		// Wait for tick.
		curentTime := <-tick

		for host, state := range serversState {
			if curentTime.Unix()-state.LastSeenTime > serverTimeout {
				// Remove server if old seen.
				log.Printf("Node %s removed from list. Curent: %d, LastSeen: %d, diff: %d", host, curentTime.Unix(), state.LastSeenTime, curentTime.Unix()-state.LastSeenTime)
				delete(serversState, host)
				continue
			}
			// Try to get curent config version.
			vr, err := getServerConfVersion(host)
			serversState[host].CurentConfVersion = vr
			serversState[host].LastErr = err

			// If CurentConfVersion or ReceivedConfVersion changed from last check (only for logs)
			if stateOld, ok := serversStateOld[host]; ok {
				if serversState[host].CurentConfVersion != stateOld.CurentConfVersion {
					log.Printf("Node: %s. Config version changed: %d -> %d", host, stateOld.CurentConfVersion, serversState[host].CurentConfVersion)
				}
				if serversState[host].ReceivedConfVersion != stateOld.ReceivedConfVersion {
					log.Printf("Node: %s. Received config version changed: %d -> %d", host, stateOld.ReceivedConfVersion, serversState[host].ReceivedConfVersion)
				}
			}
			serversStateOld[host] = &ServerInfo{0, 0, state.CurentConfVersion, state.ReceivedConfVersion, nil}
		}
	}
}

// Return readable info about all registered lb nodes.
func getServersStatus(ver int) string {
	var result string
	for host, state := range serversState {
		received := "no"
		updated := "no"
		if state.ReceivedConfVersion >= ver {
			received = "yes"
		}
		if state.CurentConfVersion >= ver {
			updated = "yes"
		}
		result += fmt.Sprintf("Node: %s; received: %s; updated: %s\n", host, received, updated)
	}
	return result
}

// Return fool info about all registered lb nodes.
func getServersStatusFull() string {
	if len(serversState) == 0 {
		return fmt.Sprintf("No registered nodes.")
	}
	var result string = fmt.Sprintf("Registered nodes count: %d\n\n", len(serversState))
	for host, state := range serversState {
		seenSecAgo := time.Now().Unix() - state.LastSeenTime
		receivedSecAgo := time.Now().Unix() - state.LastConfReceivedTime
		result += fmt.Sprintf("Node: %s\n", host)
		result += fmt.Sprintf(" last seen (seconds ago): %d\n config received (seconds ago): %d\n", seenSecAgo, receivedSecAgo)
		result += fmt.Sprintf(" curent config version: %d\n last received config version: %d\n\n", state.CurentConfVersion, state.ReceivedConfVersion)
	}
	return result
}

// Endpoint /status
// Return all info about registered nodes.
// TODO: options to change format: plain, json, prom ...
func srvStatusHandler(w http.ResponseWriter, r *http.Request) {
	v, err := getConsulConfVersion()
	if err != nil {
		http.Error(w, "Error. Can't get config version. See controller logs", 403)
		return
	}
	w.Write([]byte(fmt.Sprintf("Consul config version: %d\n", v)))
	w.Write([]byte(getServersStatusFull()))
}

// Wait for all registered lb nodes are receive and update configs to version 'ver int' or hight.
// Or return after timeout.
func waitForReload(timeout int64, ver int) {
	// Ticker for pause.
	tm := time.After(time.Duration(timeout) * time.Second)
	var tick = make(<-chan time.Time)
	tick = time.Tick(2 * time.Second)
	for {
		select {
		case <-tm:
			return
		// Wait for tick.
		case <-tick:
			var counter int = 0
			for _, state := range serversState {
				if state.CurentConfVersion >= ver {
					continue
				}
				counter++
			}
			if counter == 0 {
				// All servers are received configs and loaded it.
				return
			}
		}
	}
}

// Set http handlers and start http listener.
func startListen() {
	router := mux.NewRouter()
	router.HandleFunc("/getconf", sendConfHandler).Methods("GET")
	router.HandleFunc("/update", updateConfHandler).Methods("GET")
	router.HandleFunc("/reg", nginxRegisterHandler).Methods("GET")
	router.HandleFunc("/status", srvStatusHandler).Methods("GET")
	listenUrl := fmt.Sprintf("0.0.0.0:%s", *listenPort)
	log.Printf("Runing listener on %s", listenUrl)
	log.Fatal(http.ListenAndServe(listenUrl, router))
}

// Helper for args parse.
func getEnv(key string, defaultVal string) string {
	if envVal, ok := os.LookupEnv(key); ok {
		return envVal
	}
	return defaultVal
}

// Query to update configuration on all lb nodes.
// 1) Generate new config pack from consul metadata.
// 2) Update consul config version to notify all lb nodes that they need to update configs.
func updateConfHandler(w http.ResponseWriter, r *http.Request) {

	version, err := incrConsulConfVersion()
	w.Write([]byte(fmt.Sprintf("Incr conf version in consul: ok. New version: %d\n", version)))
	if err != nil {
		// Error with consul.
		http.Error(w, err.Error(), 403)
		return
	}
	projectsMetadataJson, err := getConsulKvJson("clients")
	projectsMetadata := parseConsulProjectsData(projectsMetadataJson)
	err = genConfig(projectsMetadata)

	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	// Write respond part to send buffer.
	w.Write([]byte("Create config from template: ok\n"))
	err = pkgConfigs(version)
	if err != nil {
		// Error with pkg configs.
		http.Error(w, err.Error(), 403)
		return
	}
	// Write respond part to send buffer.
	w.Write([]byte(fmt.Sprintf("Create configs pkg: ok\n")))

	// TODO: configure wait time.
	// Wait for all registered lb nodes receive configs and reload nginx. (But no longer than 3 minutes)
	waitForReload(30, version)
	// Write all registered nodes status.
	w.Write([]byte(getServersStatus(version)))
}

// Send GET request to nginx lb "version url" /config_version to get curent config version loaded by nginx
func getServerConfVersion(hostname string) (version int, err error) {
	// Consul url to get current config version.
	verGetUrl := "http://" + hostname + "/config_version"
	// Send request to consul API.
	resp, err := http.Get(verGetUrl)

	// Check error.
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	// Read version from http respond
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	// Convert version to int.
	ver, err := strconv.Atoi(string(body))
	if err != nil {
		return 0, err
	}
	// Return config version.
	// log.Printf("Host %s, config version updated: %d", hostname, ver)
	return ver, nil
}

// Endpoint for nginx lb registration. Client ip were added to list (check version, waiting for update)
func nginxRegisterHandler(w http.ResponseWriter, r *http.Request) {
	// get nginx lb ip (client ip)
	serverIP, _, _ := net.SplitHostPort(r.RemoteAddr)
	vr, err := getServerConfVersion(serverIP)
	// If ip is not in the list - add
	if _, ok := serversState[serverIP]; !ok {
		log.Printf("Node %s added to nodes list.", serverIP)
		serversState[serverIP] = &ServerInfo{time.Now().Unix(), 0, vr, 0, err}
		return
	}
	// Update server info
	serversState[serverIP].LastSeenTime = time.Now().Unix()
	serversState[serverIP].CurentConfVersion = vr
	serversState[serverIP].LastErr = err
}

// Endpoint to get configs pack (gzip of all nginx conf.d directory)
// Request example: http://controller-host:8081/getconf?ver=12345
func sendConfHandler(w http.ResponseWriter, r *http.Request) {
	// get requested version number
	var version string = r.URL.Query().Get("ver")
	// check requester param
	if version == "" {
		log.Println("Url Param 'ver' is missing")
		http.Error(w, "Missing version.", 404)
		return
	}
	iVersion, err := strconv.Atoi(version)
	if err != nil {
		log.Println("Url Param 'key' is not a number")
		http.Error(w, "Missing version number.", 404)
		return
	}
	// Check if config pack exists
	ConfigsPkgFile, err := os.Open(*configsPkgsDir + "/" + version + ".tar.gz")
	// Close file after return (see defer)
	defer ConfigsPkgFile.Close() //Close after function return
	if err != nil {
		//File not found, send 404
		http.Error(w, "File not found. Try again later.", 404)
		return
	}

	//Get the Content-Type of the file
	//Create a buffer to store the header of the file in
	FileHeader := make([]byte, 512)
	//Copy the headers into the FileHeader buffer
	ConfigsPkgFile.Read(FileHeader)
	//Get content type of file
	FileContentType := http.DetectContentType(FileHeader)

	//Get the file size
	FileStat, _ := ConfigsPkgFile.Stat()               //Get info from file
	FileSize := strconv.FormatInt(FileStat.Size(), 10) //Get file size as a string

	//Send the headers
	w.Header().Set("Content-Disposition", "attachment; filename="+"go.sum")
	w.Header().Set("Content-Type", FileContentType)
	w.Header().Set("Content-Length", FileSize)

	//Send the file
	//We read 512 bytes from the file already, so we reset the offset back to 0
	ConfigsPkgFile.Seek(0, 0)
	io.Copy(w, ConfigsPkgFile) //'Copy' the file to the client
	// Get server (receiver) ip
	serverIP, _, _ := net.SplitHostPort(r.RemoteAddr)
	log.Printf("Request from host: %s, version: %s", serverIP, version)
	// Update server info
	if _, ok := serversState[serverIP]; !ok {
		// Add if not exists
		log.Printf("Node %s not in a nodes list, adding.", serverIP)
		serversState[serverIP] = &ServerInfo{time.Now().Unix(), time.Now().Unix(), 0, iVersion, nil}
		return
	}
	// Update info
	serversState[serverIP].LastSeenTime = time.Now().Unix()
	serversState[serverIP].LastConfReceivedTime = time.Now().Unix()
	serversState[serverIP].ReceivedConfVersion = iVersion
}

// Create gzip of configs directory (configs pkg).
func pkgConfigs(version int) (Error error) {
	pkgName := fmt.Sprintf("%s/%d.tar.gz", *configsPkgsDir, version)
	err := compress(*configsDir, pkgName)
	if err != nil {
		log.Println(err.Error())
		return err
	} 
	log.Printf("Configs pack created: %s\n", pkgName)
	return nil
}

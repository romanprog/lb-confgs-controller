package main

import (
	"encoding/base64"
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type consulCliData []struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// http GET - get curent config version from consul kv.
func getConsulConfVersion() (Version int, Error error) {
	// Consul url to get current config version.
	verGetUrl := "http://" + *consulUrl + "/v1/kv/" + *configVersionKey + "?raw"
	// Send request to consul API.
	resp, err := http.Get(verGetUrl)

	// Check error.
	if err != nil {
		log.Printf("Error get config version from consul: %s, check url: %s", err.Error(), verGetUrl)
		return 0, err
	}
	// Close body after return (see defer)
	defer resp.Body.Close()

	// Read version from http respond
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error get config version from consul: %s, check url: %s", err.Error(), verGetUrl)
		return 0, err
	}
	// Convert version to int.
	version, err := strconv.Atoi(string(body))
	if err != nil {
		log.Printf("Error get config version from consul: %s, check url: %s", err.Error(), verGetUrl)
		return 0, err
	}
	// Return config version.
	return version, nil
}

// http GET - get recurse JSON data of 'keyname'.
func getConsulKvJson(keyname string) (data []byte, Error error) {
	// Consul url to get current config version.
	consulGetUrl := "http://" + *consulUrl + "/v1/kv/" + keyname + "?recurse"
	// Send request to consul API.
	resp, err := http.Get(consulGetUrl)

	// Check error.
	if err != nil {
		log.Printf("Error get data from consul: %s, check url: %s", err.Error(), consulGetUrl)
		return nil, err
	}
	// Close body after return (see defer)
	defer resp.Body.Close()

	// Read data from http respond
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error get data from consul: %s, check url: %s", err.Error(), consulGetUrl)
		return nil, err
	}
	// Return config version.
	return body, nil
}

// Increment configs version value in consul, using consul REST API.
func incrConsulConfVersion() (Version int, Error error) {
	// get curent version
	version, err := getConsulConfVersion()
	if err != nil {
		return 0, err
	}
	// increment version
	version = version + 1
	// POST request to put updated version to consul kv.
	verPutUrl := "http://" + *consulUrl + "/v1/kv/" + *configVersionKey
	// Init http cli
	client := &http.Client{}
	// Prepere POST request
	req, err := http.NewRequest(http.MethodPut, verPutUrl, strings.NewReader(strconv.Itoa(version)))
	if err != nil {
		log.Println(err.Error())
		return 0, err
	}
	// Sent post request.
	_, err = client.Do(req)
	if err != nil {
		log.Println(err.Error())
		return 0, err
	}
	time.Sleep(1 * time.Second)
	log.Printf("Config version increased. New version: %d\n", version)
	return version, nil
}

func parseConsulProjectsData(consulData []byte) projectsMetadataType {
	start := time.Now()
	consulData, err := getConsulKvJson("clients")
	elapsed := time.Since(start)
	if err != nil {
		return nil
	}
	log.Printf("Receiving data from consul time: %s", elapsed)
	start = time.Now()
	var parsedData = consulCliData{}
	err = json.Unmarshal(consulData, &parsedData)
	elapsed = time.Since(start)
	if err != nil {
		log.Printf("Can't parse data: %s", err.Error())
		return nil
	}
	log.Printf("Parsing data from time: %s", elapsed)
	var projects = make(projectsMetadataType)
	log.Println("Parsing: ...")

	start = time.Now()
	for _, cvalue := range parsedData {
		key := cvalue.Key

		valData, err := base64.StdEncoding.DecodeString(cvalue.Value)
		if err != nil {
			log.Printf("Consul value base64 decoe ERR: %s", err.Error())
		}
		var val = string(valData)
		splitedKey := strings.Split(key, "/")
		if splitedKey[1] == "list" {
			continue
		}
		clientUuid := splitedKey[1]
		if _, ok := projects[clientUuid]; !ok {
			projects[clientUuid] = &projectMetadata{}
			projects[clientUuid].Domains = make(map[string]*projectDomainData)
		}
		switch splitedKey[2] {
		case "var":
			switch splitedKey[3] {
			case "CACHE_URL":
				projects[clientUuid].CacheUrl = val
			case "DATABASE_SLAVE_URL":
				projects[clientUuid].DbSlaveUrl = val
			case "DATABASE_URL":
				projects[clientUuid].DbMasterUrl = val
			case "LOG_URL":
				projects[clientUuid].LogUrl = val
			case "SESSION_URL":
				projects[clientUuid].SessionsUrl = val
			case "core_path":
				projects[clientUuid].CorePath = val
			case "dev_mode":
				projects[clientUuid].DevMode = val
			case "frontend_path":
				projects[clientUuid].FrontendPath = val
			case "root_path":
				projects[clientUuid].RootPath = val
			}
		case "version":
			projects[clientUuid].Version = val
		case "domains":
			if splitedKey[3] == "list" {
				domain := splitedKey[4]
				if _, ok := projects[clientUuid].Domains[domain]; !ok {
					projects[clientUuid].Domains[domain] = &projectDomainData{false, 0}
				}
				continue
			}
			domain := splitedKey[3]
			if _, ok := projects[clientUuid].Domains[domain]; !ok {
				projects[clientUuid].Domains[domain] = &projectDomainData{false, 0}
			}
			if len(splitedKey) == 6 && splitedKey[5] == "redirect" && val == "yes" {
				projects[clientUuid].Domains[domain].Redirect = true
			}
			if len(splitedKey) == 5 && splitedKey[4] == "ssl" {
				switch val {
				case "auto":
					projects[clientUuid].Domains[domain].SslType = 1
				case "manual":
					projects[clientUuid].Domains[domain].SslType = 2
				default:
					projects[clientUuid].Domains[domain].SslType = 0
				}
				projects[clientUuid].Domains[domain].Redirect = true
			}

		case "storage":
			projects[clientUuid].Storage = val

		}

	}
	elapsed = time.Since(start)
	log.Printf("Parsing data to struct: %s", elapsed)

	return projects
}

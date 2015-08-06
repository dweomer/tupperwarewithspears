package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/Sirupsen/logrus"
	"github.com/samalba/dockerclient"
)

const (
	VERSION = "v0.1.0"
	BANNER  = ` _                
| |___      _____ 
| __\ \ /\ / / __|
| |_ \ V  V /\__ \
 \__| \_/\_/ |___/
 Tupperware with Spears (A DDoS Production)
 Author:	Jess Frazelle
 Email:		no-reply@butts.com
 Version:	` + VERSION + `

 tws [options] [http[s]://]hostname[:port]/path`
)

var (
	dockerHost string

	tlscert string
	tlskey  string

	count       int
	containers  []string
	concurrency int
	requests    int

	authHeader  string
	proxyAuth   string
	contentType string
	method      string
	protocol    string

	cookies []string
	headers []string

	timelimit int
	timeout   int
	verbosity int

	debug   bool
	version bool

	wg sync.WaitGroup

	// Client TLS cipher suites (dropping CBC ciphers for client preferred suite set)
	clientCipherSuites = []uint16{
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	}
)

func init() {
	// parse flags
	flag.StringVar(&dockerHost, "dockerHost", "unix://var/run/docker.sock", "docker daemon socket to connect to")

	flag.StringVar(&tlscert, "tlscert", "", "path to TLS certificate file")
	flag.StringVar(&tlskey, "tlskey", "", "path to TLS key file")

	flag.IntVar(&count, "nc", 16, "number of containers (tupperware) to attack with")
	flag.IntVar(&concurrency, "c", 100, "number of multiple requests to perform at a time. Default is one request at a time")
	flag.IntVar(&requests, "n", 10000, "number of requests to perform for the benchmarking session")

	flag.StringVar(&authHeader, "A", "", "auth-username:password")
	flag.StringVar(&proxyAuth, "P", "", "proxy-auth-username:password")
	flag.StringVar(&contentType, "T", "", "content type")
	flag.StringVar(&method, "m", "GET", "method")
	flag.StringVar(&protocol, "f", "ALL", "specify SSL/TLS protocol (SSL2, SSL3, TLS1, or ALL)")

	cookie := flag.String("C", "", "cookie-name=value;cookie-name=value")
	header := flag.String("H", "", "custom-header;custom-header")

	flag.IntVar(&timelimit, "t", 0, "timelimit, implies a -n 50000 internally")
	flag.IntVar(&timeout, "s", 30, "timeout, seconds to max. wait for each respone")

	flag.IntVar(&verbosity, "v", 3, "verbosity, 4 -> headers, 3 -> response codes, 2 -> warnings/info")

	flag.BoolVar(&debug, "d", false, "run in debug mode")
	flag.BoolVar(&version, "version", false, "print version and exit")

	flag.Parse()

	if *header != "" {
		headers = strings.Split(*header, ";")
	}
	if *cookie != "" {
		cookies = strings.Split(*cookie, ";")
	}
}

func main() {
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, fmt.Sprintf("%s\n\n Usage of tws:\n", BANNER))
		flag.PrintDefaults()
	}

	// set log level
	if debug {
		logrus.SetLevel(logrus.DebugLevel)
	}

	if version {
		fmt.Println(VERSION)
		return
	}

	if flag.NArg() < 1 {
		logrus.Infof("you need to pass a url to throw spears at")
		flag.Usage()
		os.Exit(1)
	}

	uri, err := url.ParseRequestURI(flag.Args()[0])
	if err != nil {
		logrus.Fatal(err)
		return
	}

	// set up tls if passed
	var tlsConfig *tls.Config = nil
	if tlskey != "" && tlscert != "" {
		tlsCert, err := tls.LoadX509KeyPair(tlscert, tlskey)
		if err != nil {
			logrus.Fatalf("Could not load X509 key pair: %v. Make sure the key is not encrypted", err)
			return
		}

		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{tlsCert},

			// Prefer TLS1.2 as the client minimum
			MinVersion:   tls.VersionTLS12,
			CipherSuites: clientCipherSuites,
		}
	}

	// init the docker client
	docker, err := dockerclient.NewDockerClient(dockerHost, tlsConfig)
	if err != nil {
		logrus.Fatal(err)
		return
	}

	// make sure we remove all containers on exit
	removeAllContainers := func() {
		for _, id := range containers {
			if err := docker.RemoveContainer(id, true, true); err != nil {
				logrus.Warnf("Failed removing container (%s): %v", id[0:7], err)
			}
		}
	}
	defer removeAllContainers()

	// watch for signal to handle ^C
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	signal.Notify(c, syscall.SIGTERM)
	go func() {
		<-c
		// sig is a ^C, handle it
		// force remove all the tupperware with spears
		logrus.Infof("Received SIGTERM, removing all tupperware with spears...")
		removeAllContainers()
		os.Exit(1)
	}()

	// create each tupperware and give it a spear
	for i := 1; i <= count; i++ {
		wg.Add(1)

		go createTupperware(i, uri, docker)
	}

	wg.Wait()
}

func createTupperware(i int, uri *url.URL, docker *dockerclient.DockerClient) {
	defer wg.Done()

	logrus.Infof("Giving tupperware container %d some spears", i)

	// create the command flags to pass to ab
	cmd := []string{
		"-c",
		strconv.Itoa(concurrency),
		"-n",
		strconv.Itoa(requests),
		"-m",
		strings.ToUpper(method),
		"-s",
		strconv.Itoa(timeout),
		"-v",
		strconv.Itoa(verbosity),
		"-f",
		protocol,
	}

	if authHeader != "" {
		cmd = append(cmd, []string{"-A", authHeader}...)
	}
	if proxyAuth != "" {
		cmd = append(cmd, []string{"-P", proxyAuth}...)
	}
	if contentType != "" {
		cmd = append(cmd, []string{"-T", contentType}...)
	}
	if timelimit > 0 {
		cmd = append(cmd, []string{"-t", strconv.Itoa(timelimit)}...)
	}
	if len(headers) > 0 {
		for _, header := range headers {
			cmd = append(cmd, []string{"-H", header}...)
		}
	}
	if len(cookies) > 0 {
		for _, cookie := range cookies {
			cmd = append(cmd, []string{"-C", cookie}...)
		}
	}

	// append the uri to the cmd string
	// make sure there is a trailing slash if none given
	if uri.Path == "" {
		uri.Path = "/"
	}
	cmd = append(cmd, uri.String())

	// create the container
	containerConfig := &dockerclient.ContainerConfig{
		Image: "jess/ab",
		Cmd:   cmd,
	}
	name := fmt.Sprintf("tws_%d", i)
	id, err := docker.CreateContainer(containerConfig, name)
	if err != nil {
		logrus.Errorf("Error while creating container (%s): %v", name, err)
		return
	}
	containers = append(containers, id)

	// start the container
	hostConfig := &dockerclient.HostConfig{}
	if err = docker.StartContainer(id, hostConfig); err != nil {
		logrus.Errorf("Error while starting container (%s): %v", name, err)
		return
	}

	// get container logs
	logOptions := &dockerclient.LogOptions{
		Follow:     true,
		Stdout:     true,
		Stderr:     true,
		Timestamps: false,
	}
	logs, err := docker.ContainerLogs(id, logOptions)
	if err != nil {
		logrus.Errorf("Error streaming container's logs (%s): %v", name, err)
		return
	}

	// read the logs
	body, err := ioutil.ReadAll(logs)
	if err != nil {
		logrus.Error("Error reading container's logs (%s): %v", name, err)
		return
	}
	logrus.Infof("Logs for container (%s): %s", name, string(body))
}

package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const APP_VENDOR = "OpenGraal"
const APP_NAME = "GS2Emu"
const APP_VERSION = "3.0.9-GO"
const APP_CREDITS = "Terry A. Davis"

var serverDir = flag.String("server", "", "")
var serverName = flag.String("name", "", "")
var port = flag.String("port", "", "")
var serverIP, localIP, serverInterface string
var mainLogger *Logger

func init() {
	flag.StringVar(&serverIP, "serverip", "", "")
	flag.StringVar(&localIP, "localip", "", "")
	flag.StringVar(&serverInterface, "interface", "", "")
}
func formatTime() string { return time.Now().Format("[03:04 PM] ") }
func logServer(msg string) {
	if mainLogger != nil {
		mainLogger.Write(msg)
	} else {
		fmt.Printf("%s%s\n", formatTime(), msg)
	}
}
func main() {
	flag.Parse()
	mainLogger = NewLogger("", true)
	mainLogger.Open("GServer.log")
	mainLogger.Write("%s %s", APP_VENDOR, APP_NAME)
	mainLogger.Write(APP_VERSION)
	mainLogger.Write("Programmed by %s.", APP_CREDITS)
	server := "default"
	if *serverDir != "" {
		server = *serverDir
	} else {
		logServer(":: Determining the server to start... ")
		if data, err := os.ReadFile("startupserver.txt"); err == nil && len(data) > 0 {
			server = trimSpace(string(data))
			logServer("success! (startupserver.txt)")
		} else {
			entries, _ := os.ReadDir("servers")
			if len(entries) == 1 {
				server = entries[0].Name()
				logServer("success! (directory search)")
			} else {
				logServer("FAILED!")
				os.Exit(1)
			}
		}
	}
	srv := NewServer("GServer-Go")
	srv.config.basePath = "servers/" + server + "/"
	srv.logger = mainLogger
	srv.logger.Write(":: Starting server: %s.", server)
	if err := srv.Init(serverIP, *port, localIP, serverInterface); err != nil {
		srv.logger.Error("Failed to start server: %s: %v", server, err)
		os.Exit(1)
	}
	srv.loadSettings()
	if *serverName != "" {
		srv.name = *serverName
	}
	displayName := srv.name
	if srv.settings.Get("name") != "" {
		displayName = srv.settings.Get("name")
	}
	srv.logger.Write(":: Started server %s (%s)", server, displayName)
	srv.logger.Write(":: Press CTRL+C to close the program.  DO NOT CLICK THE X, you will LOSE data!")
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	errChan := make(chan error, 1)
	go func() { errChan <- srv.Run() }()
	select {
	case <-sigChan:
		srv.logger.Write(":: The server is now shutting down...\n-------------------------------------\n")
		srv.Stop()
	case err := <-errChan:
		if err != nil {
			srv.logger.Error("Server error: %v", err)
		}
	}
}

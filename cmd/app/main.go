package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	geo "myq-teslamate-geofence/internal/geo"
	t "myq-teslamate-geofence/internal/types"

	"gopkg.in/yaml.v3"
)

var (
	debug      bool
	configFile string
	Config     t.ConfigStruct
	GetDevices bool
)

func init() {
	log.SetOutput(os.Stdout)
	parseArgs()
	if !GetDevices {
		loadConfig()
	}
	checkEnvVars()
	for _, car := range Config.Cars {
		car.AtHome = true // set default to true
	}
}

// parse args
func parseArgs() {
	// set up flags for parsing args
	flag.StringVar(&configFile, "config", "", "location of config file")
	flag.StringVar(&configFile, "c", "", "location of config file")
	flag.BoolVar(&Config.Testing, "testing", false, "test case")
	flag.BoolVar(&GetDevices, "d", false, "get myq devices")
	flag.Parse()

	// only check for config if not getting devices
	if !GetDevices {
		// if -c or --config wasn't passed, check for CONFIG_FILE env var
		// if that fails, check for file at default location
		if configFile == "" {
			var exists bool
			if configFile, exists = os.LookupEnv("CONFIG_FILE"); !exists {
				log.Fatalf("Config file must be defined with '-c' or 'CONFIG_FILE' environment variable")
			}
		}

		// check that ConfigFile exists
		if _, err := os.Stat(configFile); err != nil {
			log.Fatalf("Config file %v doesn't exist!", configFile)
		}
	}
}

// load yaml config
func loadConfig() {
	yamlFile, err := os.ReadFile(configFile)
	if err != nil {
		log.Fatalf("Could not read config file: %v", err)
	}

	err = yaml.Unmarshal(yamlFile, &Config)
	if err != nil {
		log.Fatalf("Could not load yaml from config file, received error: %v", err)
	}
	log.Println("Config loaded successfully")
}

func main() {
	if GetDevices {
		geo.GetGarageDoorSerials(Config)
		return
	}
	if value, exists := os.LookupEnv("TESTING"); exists {
		Config.Testing, _ = strconv.ParseBool(value)
	}
	if value, exists := os.LookupEnv("DEBUG"); exists {
		debug, _ = strconv.ParseBool(value)
	}
	fmt.Println()

	// create a new MQTT client
	opts := mqtt.NewClientOptions()
	opts.SetOrderMatters(false)
	opts.AddBroker(fmt.Sprintf("tcp://%s:%d", Config.Global.MqttHost, Config.Global.MqttPort))
	opts.SetClientID(Config.Global.MqttClientID)

	// create a new MQTT client object
	client := mqtt.NewClient(opts)

	// connect to the MQTT broker
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		log.Fatalf("could not connect to mqtt broker: %v", token.Error())
	} else {
		log.Println("Connected to MQTT broker")
	}

	messageChan := make(chan mqtt.Message)

	// create channels to receive messages
	for _, car := range Config.Cars {
		log.Printf("Subscribing to MQTT geofence, latitude, and longitude topics for car %d", car.CarID)

		if token := client.Subscribe(
			fmt.Sprintf("teslamate/cars/%d/geofence", car.CarID),
			0,
			func(client mqtt.Client, message mqtt.Message) {
				messageChan <- message
			}); token.Wait() && token.Error() != nil {
			log.Fatalf("%v", token.Error())
		}

		if token := client.Subscribe(
			fmt.Sprintf("teslamate/cars/%d/latitude", car.CarID),
			0,
			func(client mqtt.Client, message mqtt.Message) {
				messageChan <- message
			}); token.Wait() && token.Error() != nil {
			log.Fatalf("%v", token.Error())
		}

		if token := client.Subscribe(
			fmt.Sprintf("teslamate/cars/%d/longitude", car.CarID),
			0,
			func(client mqtt.Client, message mqtt.Message) {
				messageChan <- message
			}); token.Wait() && token.Error() != nil {
			log.Fatalf("%v", token.Error())
		}
	}

	log.Println("Topics subscribed, listening for events...")

	// listen for incoming messages
	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, os.Interrupt, syscall.SIGTERM)

	for {
		select {
		case message := <-messageChan:
			m := strings.Split(message.Topic(), "/")
			var car *t.Car
			for _, c := range Config.Cars {
				if fmt.Sprintf("%d", c.CarID) == m[2] {
					car = c
				}
			}
			switch m[3] {
			case "geofence":
				log.Printf("Received geo for car %d: %v", car.CarID, string(message.Payload()))
			case "latitude":
				if debug {
					log.Printf("Received lat for car %d: %v", car.CarID, string(message.Payload()))
				}
				car.CurLat, _ = strconv.ParseFloat(string(message.Payload()), 64)
				go geo.CheckGeoFence(Config, car)
			case "longitude":
				if debug {
					log.Printf("Received long for car %d: %v", car.CarID, string(message.Payload()))
				}
				car.CurLng, _ = strconv.ParseFloat(string(message.Payload()), 64)
				go geo.CheckGeoFence(Config, car)
			}

		case <-signalChannel:
			log.Println("Received interrupt signal, shutting down...")
			client.Disconnect(250)
			time.Sleep(250 * time.Millisecond)
			return

		}
	}
}

// check for env vars and validate that a myq_email and myq_pass exists
func checkEnvVars() {
	// override config with env vars if present
	if value, exists := os.LookupEnv("MYQ_EMAIL"); exists {
		Config.Global.MyQEmail = value
	}
	if value, exists := os.LookupEnv("MYQ_PASS"); exists {
		Config.Global.MyQPass = value
	}
	if Config.Global.MyQEmail == "" || Config.Global.MyQPass == "" {
		log.Fatal("MYQ_EMAIL and MYQ_PASS must be defined in the config file or as env vars")
	}
}

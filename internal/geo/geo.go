package geo

import (
	"fmt"
	"log"
	"math"
	t "myq-teslamate-geofence/internal/types"
	"os"
	"time"

	"github.com/joeshaw/myq"
)

func withinGeofence(point t.Point, center t.Point, radius float64) bool {
	// Calculate the distance between the point and the center of the circle
	distance := distance(point, center)
	return distance <= radius
}

func distance(point1 t.Point, point2 t.Point) float64 {
	// Calculate the distance between two points using the haversine formula
	const radius = 6371 // Earth's radius in kilometers
	lat1 := toRadians(point1.Lat)
	lat2 := toRadians(point2.Lat)
	deltaLat := toRadians(point2.Lat - point1.Lat)
	deltaLon := toRadians(point2.Lng - point1.Lng)
	a := math.Sin(deltaLat/2)*math.Sin(deltaLat/2) + math.Cos(lat1)*math.Cos(lat2)*math.Sin(deltaLon/2)*math.Sin(deltaLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	d := radius * c
	return d
}

func toRadians(degrees float64) float64 {
	return degrees * math.Pi / 180
}

// check if outside close geo or inside open geo and set garage door state accordingly
func CheckGeoFence(config t.ConfigStruct, car *t.Car) {
	if car.OpLock {
		return
	}
	car.OpLock = true
	if car.CurLat == 0 || car.CurLng == 0 {
		car.OpLock = false
		return // need valid lat and lng to check fence
	}

	// Define a point to check
	point := t.Point{
		Lat: car.CurLat,
		Lng: car.CurLng,
	}

	var action string
	withinGeofence := withinGeofence(point, car.GarageCloseGeo.Center, car.GarageCloseGeo.Radius)

	if car.AtHome && !withinGeofence { // check if outside the close geofence, meaning we should close the door
		action = myq.ActionClose
	} else if !car.AtHome && withinGeofence {
		action = myq.ActionOpen
	}

	if action != "" {
		log.Printf("Attempting to %s garage door for car %d", action, car.CarID)
		setGarageDoor(config, car.MyQSerial, action)
		car.AtHome = !car.AtHome                                          // toggle CarAtHome status
		time.Sleep(time.Duration(config.Global.OpCooldown) * time.Minute) // keep opLock true for OpCooldown minutes to prevent flapping in case of overlapping geofences
	}

	car.OpLock = false
}

func setGarageDoor(config t.ConfigStruct, deviceSerial string, action string) error {
	s := &myq.Session{}
	s.Username = config.Global.MyQEmail
	s.Password = config.Global.MyQPass

	var desiredState string
	switch action {
	case myq.ActionOpen:
		desiredState = myq.StateOpen
	case myq.ActionClose:
		desiredState = myq.StateClosed
	}

	if config.Testing {
		log.Printf("TESTING flag set - Would attempt action %v", action)
		return nil
	}

	log.Println("Acquiring MyQ session...")
	if err := s.Login(); err != nil {
		log.SetOutput(os.Stderr)
		log.Printf("ERROR: %v\n", err)
		log.SetOutput(os.Stdout)
		return err
	}
	log.Println("Session acquired...")

	curState, err := s.DeviceState(deviceSerial)
	if err != nil {
		log.Printf("Couldn't get device state: %v", err)
		return err
	}

	log.Printf("Requested action: %v, Current state: %v", action, curState)
	if (action == myq.ActionOpen && curState == myq.StateClosed) || (action == myq.ActionClose && curState == myq.StateOpen) {
		log.Printf("Attempting action: %v", action)
		err := s.SetDoorState(deviceSerial, action)
		if err != nil {
			log.Printf("Unable to set door state: %v", err)
			return err
		}
	} else {
		log.Printf("Action and state mismatch: garage state is not valid for executing requested action")
		return nil
	}

	log.Printf("Waiting for door to %s...\n", action)

	var currentState string
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		state, err := s.DeviceState(deviceSerial)
		if err != nil {
			return err
		}
		if state != currentState {
			if currentState != "" {
				log.Printf("Door state changed to %s\n", state)
			}
			currentState = state
		}
		if currentState == desiredState {
			break
		}
		time.Sleep(5 * time.Second)
	}

	if currentState != desiredState {
		return fmt.Errorf("timed out waiting for door to be %s", desiredState)
	}

	return nil
}

func GetGarageDoorSerials(config t.ConfigStruct) error {
	s := &myq.Session{}
	s.Username = config.Global.MyQEmail
	s.Password = config.Global.MyQPass

	log.Println("Acquiring MyQ session...")
	if err := s.Login(); err != nil {
		log.SetOutput(os.Stderr)
		log.Printf("ERROR: %v\n", err)
		log.SetOutput(os.Stdout)
		return err
	}
	log.Println("Session acquired...")

	devices, err := s.Devices()
	if err != nil {
		log.Printf("Could not get devices: %v", err)
		return err
	}
	for _, d := range devices {
		log.Printf("Device Name: %v", d.Name)
		log.Printf("Device State: %v", d.DoorState)
		log.Printf("Device Type: %v", d.Type)
		log.Printf("Device Serial: %v", d.SerialNumber)
		fmt.Println()
	}

	return nil
}

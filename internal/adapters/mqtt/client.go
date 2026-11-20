// Package mqtt connects the emulator to the MQTT broker: the robot side of
// VDA 5050 and the PLC side of the AS/RS telegram protocol.
package mqtt

import (
	"fmt"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
)

type Will struct {
	Topic   string
	Payload []byte
}

// Connect opens one MQTT client. A will, when given, is published retained by the broker if the client drops.
func Connect(brokerURL, clientID string, will *Will) (pahomqtt.Client, error) {
	opts := pahomqtt.NewClientOptions().AddBroker(brokerURL).SetClientID(clientID).
		SetAutoReconnect(true).SetConnectRetry(true).SetConnectRetryInterval(2 * time.Second).SetOrderMatters(false)
	if will != nil {
		opts.SetBinaryWill(will.Topic, will.Payload, 1, true)
	}
	c := pahomqtt.NewClient(opts)
	tok := c.Connect()
	if !tok.WaitTimeout(15*time.Second) || tok.Error() != nil {
		return nil, fmt.Errorf("mqtt connect %s as %s: %v", brokerURL, clientID, tok.Error())
	}
	return c, nil
}

func publish(c pahomqtt.Client, topic string, retained bool, payload []byte) error {
	tok := c.Publish(topic, 1, retained, payload)
	if !tok.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("mqtt publish %s: timeout", topic)
	}
	return tok.Error()
}

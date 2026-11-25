package mqtt

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestQoS1PayloadOffset(t *testing.T) {
	body := []byte{0x00, 0x01, 't', 0x00, 0x07}
	body = append(body, []byte(`{"a":1}`)...)
	topic, payload, qos, id, err := DecodePublish(0x32, body)
	if err != nil {
		t.Fatal(err)
	}
	if topic != "t" || qos != 1 || id != 7 || string(payload) != `{"a":1}` {
		t.Fatalf("%s %s %d %d", topic, payload, qos, id)
	}
}

func TestConnectAndParse(t *testing.T) {
	pkt := encodeConnect("p_abcdef01", "U0000001", "access")
	first, body, err := ReadPacket(bufio.NewReader(bytes.NewReader(pkt)))
	if err != nil || first != 0x10 {
		t.Fatal(first, err)
	}
	if !bytes.Contains(body, []byte("p_abcdef01")) || !bytes.Contains(body, []byte("MQTT")) {
		t.Fatal("connect body")
	}
	push, err := Parse([]byte(`{"method":"properties_changed","params":[{"siid":3,"piid":5,"value":4}]}`))
	if err != nil || push.Kind != "properties" || len(push.Props) != 1 || push.Props[0].Key() != "3.5" {
		t.Fatal(push, err)
	}
	otc, err := Parse([]byte(`{"method":"_otc.info","params":{"hw_ver":"Linux","ap":{"ssid":"home","bssid":"aa","rssi":-40},"netif":{"localIp":"192.0.2.10","mask":"255.255.255.0","gw":"192.0.2.1"}}}`))
	if err != nil || otc.IP != "192.0.2.10" || !otc.HasRSSI || otc.RSSI != -40 {
		t.Fatal(otc, err)
	}
	id := ClientID()
	if !strings.HasPrefix(id, "p_") || len(id) != 10 {
		t.Fatal(id)
	}
}

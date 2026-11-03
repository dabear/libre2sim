package libre2

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// swiftVectors is testdata/swift_vectors.json, printed by tools/swiftvectors
// (LibreTransmitter's own Swift code). Byte strings are hex; uint16 arrays
// are JSON numbers.
type swiftVectors struct {
	Source        string `json:"source"`
	ProcessCrypto []struct {
		In  [4]uint16 `json:"in"`
		Out [4]uint16 `json:"out"`
	} `json:"processCrypto"`
	PrepareVariables []struct {
		UID string    `json:"uid"`
		X   uint16    `json:"x"`
		Y   uint16    `json:"y"`
		Out [4]uint16 `json:"out"`
	} `json:"prepareVariables"`
	PrepareVariables4 []struct {
		UID string    `json:"uid"`
		I   [4]uint16 `json:"i"`
		Out [4]uint16 `json:"out"`
	} `json:"prepareVariables4"`
	UsefulFunction []struct {
		UID string `json:"uid"`
		X   uint16 `json:"x"`
		Y   uint16 `json:"y"`
		Out string `json:"out"`
	} `json:"usefulFunction"`
	EnableStreamingParams []struct {
		UID     string `json:"uid"`
		Info    string `json:"info"`
		Code    uint32 `json:"code"`
		CmdCode byte   `json:"cmdCode"`
		Params  string `json:"params"`
	} `json:"enableStreamingParams"`
	UnlockPayload []struct {
		UID        string `json:"uid"`
		Info       string `json:"info"`
		EnableTime uint32 `json:"enableTime"`
		Count      uint16 `json:"count"`
		Payload    string `json:"payload"`
	} `json:"unlockPayload"`
	CRC16 []struct {
		Data string `json:"data"`
		CRC  uint16 `json:"crc"`
	} `json:"crc16"`
	DecryptFRAM []struct {
		Name           string `json:"name"`
		UID            string `json:"uid"`
		Info           string `json:"info"`
		Encrypted      string `json:"encrypted"`
		PlainFirst16   string `json:"plainFirst16"`
		Plain          string `json:"plain"`
		HeaderCRCValid bool   `json:"headerCRCValid"`
		BodyCRCValid   bool   `json:"bodyCRCValid"`
		FooterCRCValid bool   `json:"footerCRCValid"`
	} `json:"decryptFRAM"`
	DecryptBLE []struct {
		UID         string `json:"uid"`
		Data        string `json:"data"`
		Plain       string `json:"plain"`
		Age         uint16 `json:"age"`
		CRCVerified bool   `json:"crcVerified"`
	} `json:"decryptBLE"`
}

func loadSwiftVectors(t *testing.T) *swiftVectors {
	t.Helper()
	b, err := os.ReadFile("../testdata/swift_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v swiftVectors
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return &v
}

func h2b(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSwiftVectors(t *testing.T) {
	v := loadSwiftVectors(t)
	t.Logf("vectors from %s", v.Source)

	// count guards against a section silently going missing from the JSON.
	count := func(name string, n int) {
		if n == 0 {
			t.Errorf("%s: no vectors", name)
		}
	}

	count("processCrypto", len(v.ProcessCrypto))
	for _, c := range v.ProcessCrypto {
		if got := processCrypto(c.In); got != c.Out {
			t.Errorf("processCrypto(%04x) = %04x, Swift %04x", c.In, got, c.Out)
		}
	}

	count("prepareVariables", len(v.PrepareVariables))
	for _, c := range v.PrepareVariables {
		if got := prepareVariables(h2b(t, c.UID), c.X, c.Y); got != c.Out {
			t.Errorf("prepareVariables(%s, %#x, %#x) = %04x, Swift %04x", c.UID, c.X, c.Y, got, c.Out)
		}
	}

	count("prepareVariables4", len(v.PrepareVariables4))
	for _, c := range v.PrepareVariables4 {
		if got := prepareVariables4(h2b(t, c.UID), c.I[0], c.I[1], c.I[2], c.I[3]); got != c.Out {
			t.Errorf("prepareVariables4(%s, %04x) = %04x, Swift %04x", c.UID, c.I, got, c.Out)
		}
	}

	count("usefulFunction", len(v.UsefulFunction))
	for _, c := range v.UsefulFunction {
		got := usefulFunction(h2b(t, c.UID), c.X, c.Y)
		if hex.EncodeToString(got[:]) != c.Out {
			t.Errorf("usefulFunction(%s, %#x, %#x) = %x, Swift %s", c.UID, c.X, c.Y, got, c.Out)
		}
	}

	count("enableStreamingParams", len(v.EnableStreamingParams))
	for _, c := range v.EnableStreamingParams {
		if c.CmdCode != 0xA1 {
			t.Errorf("command code %#x, want 0xA1", c.CmdCode)
		}
		got := enableStreamingParams(h2b(t, c.UID), h2b(t, c.Info), c.Code)
		if hex.EncodeToString(got) != c.Params {
			t.Errorf("enableStreamingParams(%s, %s, %d) = %x, Swift %s", c.UID, c.Info, c.Code, got, c.Params)
		}
		d := enableStreamingD(h2b(t, c.UID), h2b(t, c.Info), c.Code)
		if hex.EncodeToString(d[:]) != c.Params[10:] {
			t.Errorf("enableStreamingD(%s, %s, %d) = %x, Swift %s", c.UID, c.Info, c.Code, d, c.Params[10:])
		}
	}

	count("unlockPayload", len(v.UnlockPayload))
	for _, c := range v.UnlockPayload {
		got := unlockPayload(h2b(t, c.UID), h2b(t, c.Info), c.EnableTime, c.Count)
		if hex.EncodeToString(got[:]) != c.Payload {
			t.Errorf("unlockPayload(%s, %s, %d, %d) = %x, Swift %s", c.UID, c.Info, c.EnableTime, c.Count, got, c.Payload)
		}
	}

	count("crc16", len(v.CRC16))
	for _, c := range v.CRC16 {
		if got := libreCRC16(h2b(t, c.Data)); got != c.CRC {
			t.Errorf("libreCRC16(%.20s…) = %04x, Swift %04x", c.Data, got, c.CRC)
		}
	}

	count("decryptFRAM", len(v.DecryptFRAM))
	samples := map[string][3][]byte{
		"Example":               {exampleUID, exampleInfo, exampleFRAM},
		"Example2":              {example2UID, example2Info, example2FRAM},
		"FakeSensorPairingData": {xdripUID, xdripInfo, xdripFRAM},
	}
	for _, c := range v.DecryptFRAM {
		uid, info, enc := h2b(t, c.UID), h2b(t, c.Info), h2b(t, c.Encrypted)
		if s, ok := samples[c.Name]; !ok || !bytes.Equal(s[0], uid) || !bytes.Equal(s[1], info) || !bytes.Equal(s[2], enc) {
			t.Errorf("decryptFRAM %s: sample in examples_test.go differs from the Swift one", c.Name)
		}
		got := cryptFRAM(uid, info, enc)
		if hex.EncodeToString(got[:16]) != c.PlainFirst16 {
			t.Errorf("cryptFRAM %s first 16 = %x, Swift %s", c.Name, got[:16], c.PlainFirst16)
		}
		if hex.EncodeToString(got) != c.Plain {
			t.Errorf("cryptFRAM %s differs from Swift", c.Name)
		}
		hd, bd, ft := checkFRAMCRCs(t, got)
		if hd != c.HeaderCRCValid || bd != c.BodyCRCValid || ft != c.FooterCRCValid {
			t.Errorf("cryptFRAM %s CRC validity %v/%v/%v, Swift %v/%v/%v",
				c.Name, hd, bd, ft, c.HeaderCRCValid, c.BodyCRCValid, c.FooterCRCValid)
		}
	}

	count("decryptBLE", len(v.DecryptBLE))
	for _, c := range v.DecryptBLE {
		got, err := decryptPacket(h2b(t, c.UID), h2b(t, c.Data))
		if err != nil {
			t.Errorf("decryptPacket: %v", err)
			continue
		}
		if hex.EncodeToString(got) != c.Plain {
			t.Errorf("decryptPacket = %x, Swift %s", got, c.Plain)
		}
		if age := uint16(got[40]) | uint16(got[41])<<8; age != c.Age || !c.CRCVerified {
			t.Errorf("age %d (Swift %d), Swift crcVerified %v", age, c.Age, c.CRCVerified)
		}
		if !bytes.Equal(h2b(t, c.UID), bleUID) || !bytes.Equal(h2b(t, c.Data), blePacket) {
			t.Error("BLE sample in examples_test.go differs from the Swift one")
		}
	}
}

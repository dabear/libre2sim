package libre2

// The app's side of streaming, for clients and tests: what LibreTransmitter
// (@ 4110512) does with a sensor's BLE link.

// StreamingUnlockPayload is Libre2.streamingUnlockPayload: the 12 bytes an
// app writes to F001, with enableTime the code it enabled streaming with
// (LibreTransmitter: 42) and count its unlock count for this connection.
// sensorUID is in sensorUID order.
func StreamingUnlockPayload(sensorUID, patchInfo []byte, enableTime uint32, count uint16) []byte {
	p := unlockPayload(sensorUID, patchInfo, enableTime, count)
	return p[:]
}

// DecryptPacket is Libre2.decryptBLE: the 44-byte plaintext of a 46-byte
// packet, or an error if its CRC fails.
func DecryptPacket(sensorUID, packet []byte) ([]byte, error) {
	return decryptPacket(sensorUID, packet)
}

// BLERecord is one record of a packet as Libre2.parseBLEData reads it.
type BLERecord struct {
	Index                    int // 0…6 trend, 7…9 history
	Minute                   int // idValue: the life count it was taken at
	RawGlucose               int
	RawTemperature           int
	RawTemperatureAdjustment int
}

// BLEData is Libre2.parseBLEData's result.
type BLEData struct {
	Age         int
	Trend       []BLERecord // sorted by Minute, as parseBLEData sorts them
	History     []BLERecord
	CRCVerified bool
}

// ParseBLEData is Libre2.parseBLEData, a line-for-line port: it reads the 44
// bytes DecryptPacket returns. Records with raw glucose 0 are skipped, as
// the Swift does.
func ParseBLEData(data []byte) BLEData {
	age := int(word(data[41], data[40]))
	calculated := libreCRC16(data[:len(data)-2])
	enclosed := uint16(data[42])<<8 | uint16(data[43])

	const delay = 2
	ints := []int{0, 2, 4, 6, 7, 12, 15}
	var trend, history []BLERecord
	for i := 0; i < 10; i++ {
		raw := readBits(data, i*4, 0, 0xe)
		if raw == 0 {
			continue
		}
		rawTemperature := readBits(data, i*4, 0xe, 0xc) << 2
		adj := readBits(data, i*4, 0x1a, 0x5) << 2
		if readBits(data, i*4, 0x1f, 0x1) != 0 {
			adj = -adj
		}
		id := age
		if i < 7 {
			id -= ints[i]
		} else {
			id = ((id-delay)/15)*15 - 15*(i-7)
		}
		r := BLERecord{Index: i, Minute: id, RawGlucose: raw, RawTemperature: rawTemperature,
			RawTemperatureAdjustment: adj}
		if i < 7 {
			trend = append(trend, r)
		} else {
			history = append(history, r)
		}
	}
	sortByMinute(trend)
	sortByMinute(history)
	return BLEData{Age: age, Trend: trend, History: history, CRCVerified: calculated == enclosed}
}

// sortByMinute is a stable insertion sort; ten records at most.
func sortByMinute(r []BLERecord) {
	for i := 1; i < len(r); i++ {
		for j := i; j > 0 && r[j].Minute < r[j-1].Minute; j-- {
			r[j], r[j-1] = r[j-1], r[j]
		}
	}
}

// MgdlForRaw is GlucoseFromRaw's glucoseValueFromRaw for a record: the mg/dL
// LibreTransmitter computes, before rounding, for a raw value read with this
// calibration's temperature fields.
func (c Calibration) MgdlForRaw(raw int) float64 { return c.mgdlForRaw(raw) }

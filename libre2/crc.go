// CRC16 as used by FreeStyle Libre, ported from LoopKit/LibreTransmitter
// (LibreSensor/SensorContents/CRC.swift and the private crc16 in
// PreLibre2.swift @ 4110512), MIT License, Copyright (c) 2016 Mark Wilson and
// contributors; CRC.swift credits Uwe Petersen and CryptoSwift.

package libre2

// crc16Table is Crc.crc16table (identical to the table in PreLibre2.swift's
// private crc16): the reflected CRC-16/CCITT table, polynomial 0x8408.
var crc16Table = func() (t [256]uint16) {
	for i := range t {
		c := uint16(i)
		for j := 0; j < 8; j++ {
			if c&1 != 0 {
				c = c>>1 ^ 0x8408
			} else {
				c >>= 1
			}
		}
		t[i] = c
	}
	return t
}()

// libreCRC16 is Crc.crc16(b, seed: 0xffff), which is also what PreLibre2.swift's
// private crc16 returns: table CRC from seed 0xffff, then the 16 bits reversed,
// then the two bytes swapped.
func libreCRC16(b []byte) uint16 {
	crc := uint16(0xffff)
	for _, v := range b {
		crc = crc>>8 ^ crc16Table[(crc^uint16(v))&0xff]
	}
	var rev uint16
	for i := 0; i < 16; i++ {
		rev = rev<<1 | crc&1
		crc >>= 1
	}
	return rev>>8 | rev<<8
}

// framRegionCRCValid is Crc.hasValidCrc16InFirstTwoBytes: region[0:2] holds,
// big-endian, the CRC of region[2:].
func framRegionCRCValid(region []byte) bool {
	if len(region) < 2 {
		return false
	}
	return libreCRC16(region[2:]) == word(region[0], region[1])
}

// setFramRegionCRC writes the CRC of region[2:] into region[0:2], big-endian
// (Crc.bytesWithCorrectCRC, in place).
func setFramRegionCRC(region []byte) {
	c := libreCRC16(region[2:])
	region[0], region[1] = byte(c>>8), byte(c)
}

// packetCRCValid is Crc.hasValidCrc16InLastTwoBytes: the last two bytes hold,
// big-endian, the CRC of everything before them.
func packetCRCValid(b []byte) bool {
	n := len(b)
	if n < 2 {
		return false
	}
	return libreCRC16(b[:n-2]) == word(b[n-2], b[n-1])
}

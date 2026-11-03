// Sample data copied from LoopKit/LibreTransmitter @ 4110512 (MIT License,
// Copyright (c) 2016 Mark Wilson and contributors):
// LibreSensor/SensorContents/PreLibre2.swift (Libre2.Example, Example2,
// BLEExample) and LibreTransmitter/Mocks/FakeSensorPairingData.swift (the xDrip
// sample). UIDs are in sensorUID order.

package libre2

import "encoding/hex"

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

var (
	// Libre2.Example
	exampleUID = mustHex(
		"9d81c20000a407e0")
	exampleInfo = mustHex(
		"9d0830017317")
	exampleFRAM = mustHex(
		"069add798e9af4baa2554f31eae0473abd797b271ca286f85f041ccb1b524c775262bdb79397200d499ed6a78f02b616" +
			"45bc49db079fb3a9ed4f20bd25d320a6bf96ab3c8f8f016959c562fa01c9153840bf3a11c66c486a90fd136febbbf5d0" +
			"ef3c91016b5eeec79d5df305049a1981834b10f0d276ac0e5031210b510beedc4e5552f5043f81fed6e9e1933a9914f7" +
			"0a2695230e3ba8e0a28d0948c95a388396597e0260268c4e97c439372514f9c7a83b29d9f043c75da479ce64d67e28e7" +
			"44044cca839a6250e3ed90357d850eaec45a4eeea3c7f94a4b387f3d62b499335544eacc759ef5b928bae33269e79ba0" +
			"42b27ca2467766a1ea69fcc8c3caf61247bd967b696a69df74a08e651c972acc312c6ff5a142b21a636e888c87a7aba0" +
			"dd7309e6694214c3acced7e26bfae0f106db8bfbbd6aa17c624ebaecc8371544ab39081bdd76ce5ee29b528f2cbaad56" +
			"f8de9e61f19cfdfe")
	// Libre2.Example2
	example2UID = mustHex(
		"df20be0000a407e0")
	example2Info = mustHex(
		"9d0830017625")
	example2FRAM = mustHex(
		"520bf344dca04321cc7dd74e29e282e3e704c9cf6c572c7da88210aad73219b3c79f395fe37a4508b709bc6efada3407" +
			"b46568607ea504e665654813f89ca7c870a74d9d523586f202cc9b9b7432ffc5bfe9781f46c2c70b0fb0c85423e20d44" +
			"9744368fac12ae4a6ce137e2462b5c741b7afe674fccdd951773b325e9aba65e70e46cce568db9e5feaa503652d2c522" +
			"2439d863086204adfa89001072cfa9f3474bf57096f28acaffefa39e1aec9f4a2fe8a9cae6c8744698b2a29e8df0af09" +
			"c15b52597e00d33f59417b33eedb4051b23d9482f3b2e4caad3cd8c0d7d74c51caa3ad2624ab10ba6135e17f3d3fecb4" +
			"cfe3a2316ae7d73618215b435a9c757c89e2496cb1716a476e8ae5b2c537e9e5ddb31237957ad01f73ebb815f1e65d51" +
			"fb1688a69c17b0400ebbd7ca9dcd8b60888854fc657143e751e218ea631d5baad1d3d708b7ed87c4b42431e7a0e65951" +
			"93fda3e6bfe1f209")
	// FakeSensorPairingData (de_new_patch_uid, de_new_patch_info, de_new_packet)
	xdripUID = mustHex(
		"d6f10f0100a407e0")
	xdripInfo = mustHex(
		"9d0830019c16")
	xdripFRAM = mustHex(
		"8788d2e8dd289b95b59de11f472c614fcb815ec8364a4c1fa4c8598172bf9eaea41b515bb99f6a6bf45578e1a34f3a60" +
			"498f1fcbdf2edcbe59c9288ef48316110ad2746f9dbf2944378de9f747012b035e9b72251f8211b5db19429cfe916394" +
			"f71467b625f3f9ee3054a4892ba8e46f7a4fa3dcc043fc381c32761b17b68187f8d397cad567f64abd6f2b90d6d24e96" +
			"8798cff682b37bf1d4f23bb3c37633e5a3e927de6a21c2b2fc0287b1557cc9e05b9f636167183de9921fedad41ee8dd7" +
			"5e3d4ba420fa6c0cf768e5fb90c654494dfe1ea3252ba56ff9c0ce18676e33c1435335445291d2085a9d18ea2dcb112b" +
			"e00be3841854c0c174fb534d3a29566dce7e28040fd4b7aa194f5f605a590989a3ed24cc6f88f853d7e374076de16ee9" +
			"ed640f46580e8f306bdbd6bd56e08987514eade3630f18414552dd3e210e746bd9cf4fa39462ff6a52be1537bbadd463" +
			"2823266090e7cdf6")
	// Libre2.BLEExample
	bleUID = mustHex(
		"2fe7b10000a407e0")
	blePacket = mustHex(
		"b194faed2cdea1694657cfd0d85aaaf1e2891ce9ac8216fb67a1d3b63f91cd184b95316c045fe196c4fd14fc68e0")
)

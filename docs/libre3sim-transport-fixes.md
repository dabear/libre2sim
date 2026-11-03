# Fixes to libre3sim's transport

libre2sim's `impossible.go` is libre3sim's (`LoopKit/libre3sim` @ `ccd9a91`, `impossible.go`) with a different GATT table. Two bugs in the
shared transport were found and fixed while making it work for a Libre 2, and they are in libre3sim too. A third item is a
limit worth a line in its README. The diffs are against libre3sim's `impossible.go` and are offered for libre3sim; g7sim's transport,
which libre3sim copied, may have the same code.

## 1. The relaunch of a held app can never fire

`SimRadio.Run` tests `c != nil` (the connected client) around two cases, but the second case, the relaunch for a held connect, is for
an app that has no client: it was terminated, and iOS holds its connect and relaunches it when the sensor advertises. With
`c != nil` around both, `c == nil && r.held != nil` is false, so `-relaunch` never relaunches anything. The
"missed wake" count needs a client; the relaunch must not.

```diff
-		if advertising && c != nil && lc != r.lastLC {
-			if !r.pending && !scanning && r.link == nil && r.held == nil {
+		// A held app has no client, so only the missed-wake case may need one.
+		if advertising && lc != r.lastLC {
+			if c != nil && !r.pending && !scanning && r.link == nil && r.held == nil {
 				r.missedWakes++
 				missed = r.missedWakes
 			}
 			if c == nil && r.held != nil && now.Sub(r.held.launched) > 20*time.Second {
 				r.held.launched = now
 				relaunch = r.held
 			}
 		}
```

## 2. A link can be published before its handler exists

`tryConnect` publishes the link (`r.link = l`), then tells the app `didConnect`, and only then creates the handler
(`l.handler = ...NewHandler(...)`). `tryConnect` runs on `Run`'s goroutine and the app's first request (a service discovery, a
write) arrives on the client's, so it can reach a link with a nil handler. The handler is also assigned without a lock while the link is
readable. Create the handler before the link is visible and before the app hears about the connection, and count the link as opened
under the same lock:

```diff
 	r.pending = false
 	l := &simLink{radio: r, client: c, addr: c.addr, notifying: map[libre3.Char]string{}}
+	// The handler exists before the link is visible and before the app hears
+	// didConnect: tryConnect also runs on Run's goroutine, and the app's
+	// first request may otherwise reach a link without one.
+	l.handler = libre3.NewHandler(r.sensor, l)
 	r.link = l
+	r.sensor.LinkOpened()
 	r.mu.Unlock()

-	r.sensor.LinkOpened()
 	log.Printf("%s connected", l.addr)
 	c.send(map[string]interface{}{"type": "didConnect", "id": r.id})
-	l.handler = libre3.NewHandler(r.sensor, l)
 }
```

Counting the link as opened inside the lock also closes a window where the sensor advertises with a link up (`links == 0` while
`r.link != nil`). libre2sim's `Sensor.LinkOpened` and `LinkClosed` count, so a link that finishes closing after the next one
opened cannot leave the sensor advertising; libre3sim's sensor could take the same change.

## 3. Unix socket paths longer than 103 bytes fail

macOS limits a Unix socket path to 104 bytes, including the NUL. A long path, such as one in a session scratchpad or a
`$TMPDIR`, makes `net.Listen("unix", path)` fail with `bind: invalid argument`, a message that does not say why. Use a short
path (`/tmp/impossible-libre3.sock`) or a relative one. Worth a sentence in the README, and optionally a clearer error:

```diff
+	if len(path) >= 104 {
+		return nil, fmt.Errorf("socket path %q is %d bytes: macOS allows fewer than 104", path, len(path))
+	}
 	l, err := net.Listen("unix", path)
```

(libre2sim does not have this check; the README notes the limit. The line above is a suggestion for libre3sim.)

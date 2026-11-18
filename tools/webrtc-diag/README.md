# WebRTC recorder connection check

A single static page that runs on the operator's workstation, connects to the recorder
the same way the screen recording client does (`POST /api/webrtc/video`), and measures the
connection: media path (direct or TURN relay), RTT, packet loss, retransmits, bitrate,
CPU/bandwidth limitation, ICE disconnects.

Test sessions use the `webrtc-diag-` name prefix: the server receives and measures media
as usual, but discards the recording instead of uploading it.

## Deploy

The page must be served from the same origin as the Webitel API (no CORS), e.g. nginx:

```nginx
location /webrtc-diag/ {
    alias /usr/share/webitel/webrtc-diag/;
}
```

Copy `index.html` to that directory and open `https://<webitel-host>/webrtc-diag/`.

## Use

1. Paste the access token (`X-Webitel-Access`) of any user allowed to record.
2. Paste the ICE servers JSON used by the client (STUN/TURN), or leave it empty.
3. Start. "Force TURN relay" checks that the TURN path works on its own.
4. "Until stopped" keeps the session open for long monitoring of a flaky workstation.
5. Send the downloaded JSON report to support.

On the server, every session writes one `session stats` log line on close
(loss, NACK, jitter, RTT, selected candidate pair), find it by the session id shown on the page.

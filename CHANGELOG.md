# Changelog

## [0.4.1](https://github.com/lscythe/jalvio-service/compare/v0.4.0...v0.4.1) (2026-10-27)


### Bug Fixes

* **fleet:** take future reports as observed on receipt ([#37](https://github.com/lscythe/jalvio-service/issues/37)) ([ad8f4c7](https://github.com/lscythe/jalvio-service/commit/ad8f4c75bee14508fbab192391f1e2b182951adb))

## [0.4.0](https://github.com/lscythe/jalvio-service/compare/v0.3.0...v0.4.0) (2026-10-27)


### Features

* serve place search and reverse geocoding ([#34](https://github.com/lscythe/jalvio-service/issues/34)) ([af8637a](https://github.com/lscythe/jalvio-service/commit/af8637ab36ffc5bee76426ceb65e0670c4cfb8ff))
* stream live buses over a WebSocket ([#32](https://github.com/lscythe/jalvio-service/issues/32)) ([b1d4fc3](https://github.com/lscythe/jalvio-service/commit/b1d4fc34961d124bb2a283f1c62e782b9d32bd00))

## [0.3.0](https://github.com/lscythe/jalvio-service/compare/v0.2.0...v0.3.0) (2026-10-22)


### Features

* **catalog:** keep scheduled arrival times per trip stop ([#28](https://github.com/lscythe/jalvio-service/issues/28)) ([f00c7d9](https://github.com/lscythe/jalvio-service/commit/f00c7d9633abd6e9d55db4994c6b641f87737df8))
* **catalog:** parse fares, platform codes, and transfers ([#29](https://github.com/lscythe/jalvio-service/issues/29)) ([2952d1d](https://github.com/lscythe/jalvio-service/commit/2952d1d3057e1ab5323a8f7d0fd13f6039ddb410))
* **catalog:** parse service calendars ([#27](https://github.com/lscythe/jalvio-service/issues/27)) ([4d19b70](https://github.com/lscythe/jalvio-service/commit/4d19b70be195d9e708e9886ec22a97208aa6a6e8))
* serve live departures and buses ([#31](https://github.com/lscythe/jalvio-service/issues/31)) ([47524e4](https://github.com/lscythe/jalvio-service/commit/47524e4055bf03db529010edad9f3a30635134e5))
* serve the offline catalog ([#30](https://github.com/lscythe/jalvio-service/issues/30)) ([16cf7ad](https://github.com/lscythe/jalvio-service/commit/16cf7ada18ca2dee5290bc238d5fa901461981d7))


### Bug Fixes

* **deps:** update Go to 1.27.3 ([#25](https://github.com/lscythe/jalvio-service/issues/25)) ([bb96273](https://github.com/lscythe/jalvio-service/commit/bb9627388310bce51b79cf02bd92c8dda59cb162))

## [0.2.0](https://github.com/lscythe/jalvio-service/compare/v0.1.0...v0.2.0) (2026-10-19)


### Features

* **mqttfeed:** count skipped messages by reason ([#19](https://github.com/lscythe/jalvio-service/issues/19)) ([cefc5ea](https://github.com/lscythe/jalvio-service/commit/cefc5ea96a1a4ce9b87f2fecc9948f95c4410f4e))


### Bug Fixes

* **mqttfeed:** make the client ID prefix configurable ([#23](https://github.com/lscythe/jalvio-service/issues/23)) ([df290f1](https://github.com/lscythe/jalvio-service/commit/df290f19d69e4c735fe4abdc23323c30f6b5c4ef))

## 0.1.0 (2026-10-11)


### Features

* add service skeleton with health and status endpoints ([337b959](https://github.com/lscythe/jalvio-service/commit/337b959dd594d443ed414f63f7152877cfd10c11))
* **atomicfile:** add crash-safe file replacement ([5b1fcd7](https://github.com/lscythe/jalvio-service/commit/5b1fcd79f6ec26105680a7c2a7e1a97ccf113e55))
* **catalog:** add in-memory stop and route search ([afb4760](https://github.com/lscythe/jalvio-service/commit/afb47602fd224fd4998b4f3b193d9bac8470f7d6))
* **catalog:** parse the GTFS feed into an immutable catalog ([030d72c](https://github.com/lscythe/jalvio-service/commit/030d72c4a3f0fe329537428f3b9377732ecd1222))
* **catalog:** refresh the feed and keep the last good copy on disk ([b88ac96](https://github.com/lscythe/jalvio-service/commit/b88ac962ed12238046bbe94502aa71372709e538))
* **fleet:** track live bus state from the position feed ([#6](https://github.com/lscythe/jalvio-service/issues/6)) ([fcc25a9](https://github.com/lscythe/jalvio-service/commit/fcc25a9957aec94450804e29b1eed81dbbbe672d))
* load and refresh the GTFS catalog at startup ([e1c6037](https://github.com/lscythe/jalvio-service/commit/e1c6037a48f31601df4b4a03c73bd7890f689336))
* **places:** add destination search through a Photon geocoder ([#12](https://github.com/lscythe/jalvio-service/issues/12)) ([4e5ec3d](https://github.com/lscythe/jalvio-service/commit/4e5ec3dcbae7fa99c63751cdbf0eab6ea7bd3807))
* run the stop facilities sync and report it in status ([#11](https://github.com/lscythe/jalvio-service/issues/11)) ([19919d8](https://github.com/lscythe/jalvio-service/commit/19919d87cdad0aa1afcb2320ceac80e88900a232))
* ship a distroless container image with compose ([#13](https://github.com/lscythe/jalvio-service/issues/13)) ([aa948dc](https://github.com/lscythe/jalvio-service/commit/aa948dc3508fa50b95c39a19d9a1f51cd1a93490))
* **stopapi:** add a guest-session client for the stop API ([#9](https://github.com/lscythe/jalvio-service/issues/9)) ([ba3920f](https://github.com/lscythe/jalvio-service/commit/ba3920f85058263b786fa642be7ec35bcd140922))
* **stopinfo:** sync halte facilities from the stop API ([#10](https://github.com/lscythe/jalvio-service/issues/10)) ([7cf389d](https://github.com/lscythe/jalvio-service/commit/7cf389d7ea7c016715b23ada0a0a888fd4e11d46))
* stream live bus positions from the MQTT broker ([#7](https://github.com/lscythe/jalvio-service/issues/7)) ([7f227fb](https://github.com/lscythe/jalvio-service/commit/7f227fba3bfe2bde1d31e9bb54e0decff4ec273a))


### Bug Fixes

* **release:** start versioning at 0.1.0 ([#17](https://github.com/lscythe/jalvio-service/issues/17)) ([077e11c](https://github.com/lscythe/jalvio-service/commit/077e11c44218d2a4ba01cdf2eeeb02f7a8fc273f))

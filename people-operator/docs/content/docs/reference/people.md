---
title: Person
weight: 10
toc: true
---

<!-- Generated from deploy/people-crd.yaml by crdref. Do not edit. -->

## spec

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--displayname"></span>`displayName` | string | yes | The name a screen shows for this person. |
| <span id="spec--nickname"></span>`nickname` | string | no | A short name, one word, for a screen with little room: a first name, or what people call this person. Optional. A screen shows the display name when this is unset. |
| <span id="spec--avatar"></span>`avatar` | string | no | This person's picture, as a `claim://<claim>/<path>` reference to a file on a claim. It is the same form a `Play` uses to name media. Optional. |
| <span id="spec--uid"></span>`uid` | integer | no | The Linux uid that owns this person's files. Set it when this person's files already exist under a uid, on a NAS for example. Optional. Nothing assigns one yet. |
| <span id="spec--identity"></span>`identity` | [object](#specidentity) | no | This person's login at an outside identity provider, as an OIDC issuer URL and subject. Optional. Nothing reads it yet. |

### spec.identity

This person's login at an outside identity provider, as an OIDC issuer URL and subject. Optional. Nothing reads it yet.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specidentity--issuer"></span>`issuer` | string | yes | The issuer URL, exactly as the provider's discovery document states it. |
| <span id="specidentity--subject"></span>`subject` | string | yes | The subject claim, `sub`, that the issuer gives this person. |

## status

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--uid"></span>`uid` | integer | no | The uid in effect for this person. Nothing writes it yet. |

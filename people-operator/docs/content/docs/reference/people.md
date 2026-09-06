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
| <span id="spec--nickname"></span>`nickname` | string | no | The short name for this person, for a screen with room for one word: a first name, or what the house calls them. Optional; a screen falls back to the display name. |
| <span id="spec--avatar"></span>`avatar` | string | no | A reference to this person's picture, for a screen to draw. A `claim://<claim>/<path>` reference names a file on a claim, the way a `Play` names media. |
| <span id="spec--uid"></span>`uid` | integer | no | The Linux uid this person's files are owned by. State it for a person whose files already exist under one on a NAS. Leave it unset and a later controller assigns one from a reserved range. |
| <span id="spec--identity"></span>`identity` | [object](#specidentity) | no | An outside login this person is, as an OIDC issuer URL and subject. Nothing in liken reads it yet. |

### spec.identity

An outside login this person is, as an OIDC issuer URL and subject. Nothing in liken reads it yet.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specidentity--issuer"></span>`issuer` | string | yes | The issuer URL, as the provider's discovery document states it. |
| <span id="specidentity--subject"></span>`subject` | string | yes | The subject claim that provider issues for this person. |

## status

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--uid"></span>`uid` | integer | no | The uid this person holds, stated in the spec or assigned by a controller. |

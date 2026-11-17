# 11: Sign-in evidence

This plan is a stub.  It gives the idea, the decisions so far, and the
stages, and it will be written out in full before it is built.

The maintainer wants `transect` to detect, reliably, that an app which
handles its own identity provider integration, rather than having one
in front of it at the ingress, does have authentication turned on.  The
evidence must come from the cluster's state, not only from an
annotation on the workload that states it.

Plan 09 gave the sign-in gate three states: an object on the route puts
sign-in there (the filled dot), no object visible to `transect` does
(the open dot), or a `<prefix>/signs-in` sentence states that the
workload signs its callers in itself (the declared dot).  This plan has
a sign-in gate name the evidence behind it, strongest first, so the
declared sentence is used last.

## Decisions

### Kinds of evidence

1. **Observed.**  A blackbox probe sends a request with no credentials
   to the way in and records the answer: a redirect to the identity
   provider, a 401 or a 403, or a page.  `transect` sends no requests of
   its own.  It reads the probe's result from Prometheus through the
   `MetricsSource`, in a new query whose answers are keyed by the
   probe's target, as the traffic queries are keyed by backend.  This
   is the only evidence that covers an app whose sign-in is configured
   in files or a database on its volume, and the only one that shows
   enforcement and not just configuration.  It needs probes deployed
   in the cluster; the Prometheus Operator's `Probe` kind with the
   blackbox exporter is the example.  The blackbox exporter exports
   the status code but not the redirect's `Location`, so the sample
   module does not follow redirects and checks `Location` against the
   provider's host with `fail_if_header_not_matches`.
   `examples/kube-prometheus/` gets the query, a sample `Probe`, and
   the module.
2. **Configured.**  Many apps declare their sign-in in the pod spec:
   settings that turn on OIDC or SSO, name the provider's URL, or turn
   off password login.  Knowledge keyed by the workload's image, a new
   resource beside kind knowledge, declares which settings mean what:

   ```yaml
   apiVersion: transect.liken.sh/v1alpha1
   kind: ImageKnowledge
   metadata:
     name: an-app
   spec:
     image: example.io/an-app
     signIn:
       on: [{env: OIDC_ENABLED, equals: "true"}]
       passwordOff: [{env: PASSWORD_LOGIN, equals: "false"}]
       provider: {env: OIDC_ISSUER_URL, show: true}
   ```

   `image` is a repository without a registry host, as
   `controller.image` is.  `transect` already stores pod specs,
   including each setting's name and any literal value, and today
   renders no literal value.  This keeps that rule, with one exception
   the knowledge grants: a page shows the setting names, and shows a
   literal value only for a field marked `show`, such as the provider's
   URL.  It compares a literal to `equals` in memory and never shows it.
   A setting whose value comes from a `Secret` or a `ConfigMap` is named
   as set from that object, its value unread, since `transect` never
   reads `Secret` data.  The provider's address resolves through address
   resolution to the identity provider's workload and draws an
   `authenticates through` edge.  This shows configuration, not
   enforcement.
3. **Declared.**  Plan 09's `<prefix>/signs-in` sentence, kept, and
   shown only when neither kind above exists.

### How a gate shows its evidence

The marks stay as plan 09 drew them.  The evidence is words, in the
dot's hover and beside the box on the workload's access drawing:

- "observed: a request with no credentials is sent to {provider}"
- "observed: a request with no credentials is answered 401"
- "configured: OIDC is on, password login is off"

When two kinds disagree, such as configured sign-in and a probe that
is answered 200, both facts show side by side in plain words, with no
color and no verdict.

### What is not available

An identity provider's list of the applications it signs in is stored in
its database, not in cluster objects.  If a provider's configuration is
kept as cluster objects, such as blueprints in ConfigMaps or custom
resources, a later stage could read the provider's side of the
configuration.  `transect` reads no `ConfigMap` data today, so that
stage starts with a decision.

## Stages

1. The evidence model, with the hover and drawing wording, on the
   fixture, with the declared sentence as the only source.
2. `ImageKnowledge` and the configured evidence, with examples for a
   few public apps, such as Grafana.
3. The observed evidence through the `MetricsSource`, with the sample
   `Probe` and module.
4. Check the design against the real cluster, and stop.

## Open questions

- The image-knowledge resource's name.  `ImageKnowledge` is a
  placeholder.
- Whether `transect` should read `ConfigMap` data for an app's
  configuration, and under what rule.
- Whether an observed 200 beside a declared sign-in shows in the Now
  list.

package main

// webhookpost.go posts a walk to the addresses a Library names in
// spec.webhooks (plan 99). The close container posts after its hand-off, so a
// catalog pod holds every row the document names before a receiver reads it.
// A receiver that refuses or does not answer does not fail the Job, because
// the catalog holds the titles. The delivery is a runs row of its own worker,
// which the close container hands off as it does the phases' run, so
// status.runs shows the last delivery and the address that refused it.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The worker whose runs row records the last delivery of a Library's
// webhooks.
const workerWebhooks = "webhooks"

// The environment of the walk's containers. The scan reads whether to note the
// videos before it walks. The close container reads the addresses as one JSON
// list, and the token of each address that names a Secret from its own
// variable, which a secretKeyRef fills, so no container reads the API server.
const (
	libraryNoteVideosVariable = "LIBRARY_NOTE_VIDEOS"
	libraryWebhooksVariable   = "LIBRARY_WEBHOOKS"
	webhookTokenPrefix        = "LIBRARY_WEBHOOK_TOKEN_"
)

// The variable that carries the token of the address at one position of
// spec.webhooks.
func webhookTokenVariable(position int) string {
	return webhookTokenPrefix + strconv.Itoa(position)
}

// How long the close container tries one address, and the backoff between
// its tries. The Job holds the Library's catalog claim while it tries, so the
// Library's next Job waits for it, and the bound keeps that wait short.
const (
	webhookDeadline       = 2 * time.Minute
	webhookFirstBackoff   = time.Second
	webhookLongestBackoff = 30 * time.Second
	webhookRequestTimeout = 30 * time.Second
)

// One address of spec.webhooks as the close container reads it: its place in
// the list, which a failure names, the URL, and the bearer token, empty where
// the address names no Secret.
type webhookTarget struct {
	position int
	url      string
	token    string
}

// The addresses the close container reads from its environment. A list this
// image cannot read is no address, so the Job posts nothing.
func webhookTargetsOf(list string, token func(string) string) []webhookTarget {
	if list == "" {
		return nil
	}
	var urls []string
	if err := json.Unmarshal([]byte(list), &urls); err != nil {
		return nil
	}
	targets := make([]webhookTarget, 0, len(urls))
	for position, address := range urls {
		targets = append(targets, webhookTarget{position: position, url: address,
			token: token(webhookTokenVariable(position))})
	}
	return targets
}

// The webhooks of one close container: the addresses and the client that
// posts to them.
type webhookPoster struct {
	targets []webhookTarget
	client  *http.Client
}

func webhookPosterOf(env func(string) string) webhookPoster {
	return webhookPoster{
		targets: webhookTargetsOf(env(libraryWebhooksVariable), env),
		client:  &http.Client{Timeout: webhookRequestTimeout},
	}
}

func newWebhookPoster() webhookPoster {
	return webhookPosterOf(os.Getenv)
}

// The close container's last step after the hand-off of the phases' run:
// the walk's changed titles, posted to every address, and the delivery's own
// runs row. A Job whose scan noted no videos, or whose walk changed none,
// posts nothing and writes no row.
func (r *closeRun) postWebhooks(ctx context.Context, walk libraryRun) {
	if len(r.webhooks.targets) == 0 {
		return
	}
	changes, err := r.walkChanges(ctx)
	if err != nil {
		r.logf("posts no webhook: %v", err)
		return
	}
	if len(changes) == 0 {
		r.logf("posts no webhook: the walk added or replaced no video")
		return
	}
	body, err := r.walkBody(ctx, walk, changes)
	if err != nil {
		r.logf("posts no webhook: %v", err)
		return
	}
	payload, _ := json.Marshal(body)

	delivery := libraryRun{Worker: workerWebhooks, Job: r.job, Started: time.Now().UTC()}
	failures := r.webhooks.postAll(ctx, payload)
	delivery.Finished = time.Now().UTC()
	delivery.Failure = strings.Join(failures, "; ")
	r.logf("posted %s to %s, and %d refused it",
		counted(len(body.Titles), "title"), counted(len(r.webhooks.targets), "webhook"), len(failures))
	if err := handOff(ctx, r.catalog, r.library, delivery, r.log, r.handoffTimeout); err != nil {
		r.logf("could not hand off the delivery of the webhooks: %v", err)
	}
}

// The document of one walk.
func (r *closeRun) walkBody(ctx context.Context, walk libraryRun, changes map[string]string) (walkBody, error) {
	namespace, name, _ := strings.Cut(r.library, "/")
	titles, err := walkTitles(ctx, r.catalog, r.library, r.kind, changes)
	if err != nil {
		return walkBody{}, err
	}
	scope := walkFull
	if len(r.scanPaths) > 0 {
		scope = walkFolders
	}
	return walkBody{
		Namespace: namespace, Library: name, Kind: r.kind, Job: r.job, Walk: scope,
		Started: walk.Started, Finished: walk.Finished, Titles: titles,
	}, nil
}

// Posts one document to every address at once, so one address that does not
// answer does not hold the others. Each failure names its address by its
// position in spec.webhooks and its host, and never by its path or query,
// which can carry a key.
func (p webhookPoster) postAll(ctx context.Context, payload []byte) []string {
	failures := make([]string, len(p.targets))
	var wait sync.WaitGroup
	for at, target := range p.targets {
		wait.Go(func() {
			if err := p.post(ctx, target, payload); err != nil {
				failures[at] = fmt.Sprintf("webhook %d (%s): %v", target.position+1, hostOf(target.url), err)
			}
		})
	}
	wait.Wait()
	var refused []string
	for _, failure := range failures {
		if failure != "" {
			refused = append(refused, failure)
		}
	}
	return refused
}

// Posts to one address until it answers with a 2xx status, it refuses the
// request in a way a retry cannot change, or the deadline passes.
func (p webhookPoster) post(ctx context.Context, target webhookTarget, payload []byte) error {
	ctx, cancel := context.WithTimeout(ctx, webhookDeadline)
	defer cancel()
	backoff := webhookFirstBackoff
	for {
		final, err := p.postOnce(ctx, target, payload)
		if err == nil || final {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, webhookLongestBackoff)
	}
}

// One request. final is true for an answer that a retry cannot change: a
// 4xx status other than 408 and 429, which say to try again later.
func (p webhookPoster) postOnce(ctx context.Context, target webhookTarget, payload []byte) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.url, bytes.NewReader(payload))
	if err != nil {
		return true, fmt.Errorf("the address is not a URL")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "library-operator")
	if target.token != "" {
		request.Header.Set("Authorization", "Bearer "+target.token)
	}
	response, err := p.client.Do(request)
	if err != nil {
		return false, fmt.Errorf("sending the request: %w", unwrapURLError(err))
	}
	defer drain(response.Body)
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
	if response.StatusCode >= 200 && response.StatusCode <= 299 {
		return true, nil
	}
	final := response.StatusCode >= 400 && response.StatusCode <= 499 &&
		response.StatusCode != http.StatusRequestTimeout && response.StatusCode != http.StatusTooManyRequests
	return final, fmt.Errorf("the receiver answered %s", response.Status)
}

// The error under a url.Error, whose text repeats the whole URL.
func unwrapURLError(err error) error {
	if wrapped, ok := err.(*url.Error); ok {
		return wrapped.Err
	}
	return err
}

// The host of an address, which a failure names.
func hostOf(address string) string {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" {
		return "no host"
	}
	return parsed.Host
}

// The environment the walk's containers take for a Library that names
// webhooks. The scan notes the videos before it walks, after its copy holds
// the write the phases wait for. The close container takes the addresses, and
// each token through a secretKeyRef. A Job that does not walk changes no
// video, so it takes none of them.
func withWebhookEnv(library *Library, plan libraryJob, containers []Container) {
	if len(library.Spec.Webhooks) == 0 || plan.mode != jobModeWalk {
		return
	}
	for at := range containers {
		switch containers[at].Name {
		case scanPhase:
			containers[at].Env = append(containers[at].Env,
				EnvVar{Name: libraryNoteVideosVariable, Value: "true"},
				EnvVar{Name: syncActorVariable, Value: plan.sync.actor},
				EnvVar{Name: syncVersionVariable, Value: strconv.FormatInt(plan.sync.version, 10)},
				EnvVar{Name: syncTimeoutVariable, Value: defaultSyncTimeout.String()},
			)
		case closeMode:
			containers[at].Env = append(containers[at].Env, webhookEnv(library.Spec.Webhooks)...)
		}
	}
}

// The close container's variables: the addresses as one JSON list, and one
// variable for each address that names a Secret.
func webhookEnv(webhooks []LibraryWebhook) []EnvVar {
	urls := make([]string, 0, len(webhooks))
	for _, webhook := range webhooks {
		urls = append(urls, webhook.URL)
	}
	list, _ := json.Marshal(urls)
	env := []EnvVar{{Name: libraryWebhooksVariable, Value: string(list)}}
	for position, webhook := range webhooks {
		if webhook.SecretRef == nil {
			continue
		}
		env = append(env, EnvVar{Name: webhookTokenVariable(position), ValueFrom: &EnvVarSource{
			SecretKeyRef: &SecretKeySelector{Name: webhook.SecretRef.Name, Key: webhook.SecretRef.secretKey()},
		}})
	}
	return env
}

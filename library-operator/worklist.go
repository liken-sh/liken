package main

// worklist.go carries one heavy fact's gap from the library Job to the fact's
// worker Job, over the bus. The library Job holds a complete copy of the
// catalog, so its close container reads the gap (workgap.go) and publishes
// each video as one retained message, indexed from 0, and then the count.
// The worker is an Indexed Job of that many completions, and each of its pods
// reads only the message at its own index. A retained message costs the
// broker memory and the cluster no etcd space, and a person reads the list
// with mosquitto_sub.
//
// The broker keeps retained messages in memory, so a broker restart clears
// every list. A pod whose message is gone has nothing to work, its video
// stays in the gap, and the next library Job lists it again.

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// The most videos one list holds. Each video is one index of the worker's
// Indexed Job, and the Job controller gives each pod the hostname
// <job>-<index>, which must fit the 63 characters of a DNS label. The longest
// worker Job name the CRD admits is 58 characters, which leaves four digits
// for the index. A longer gap lists its first 10,000 videos, and the library
// Job after the worker lists the rest.
const maxWorkListLength = 10000

// Publishes every video of the list and then its count, and returns once the
// broker holds the count. The broker reads the session's publishes in order,
// so a count it holds proves that it holds every video before it.
func publishWorkList(session *busSession, base string, list workList, items []workItem) error {
	for index, item := range items {
		payload, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if err := session.retain(list.itemTopic(base, index), payload); err != nil {
			return err
		}
	}
	count := strconv.Itoa(len(items))
	if err := session.retain(list.countTopic(base), []byte(count)); err != nil {
		return err
	}
	held, _, err := session.readRetained(list.countTopic(base))
	if err != nil {
		return err
	}
	if string(held) != count {
		return fmt.Errorf("the broker holds the count %q after the publish of %s", held, count)
	}
	return nil
}

// The video at one index of a list, and whether the broker holds one.
func readWorkItem(session *busSession, base string, list workList, index int) (workItem, bool, error) {
	payload, held, err := session.readRetained(list.itemTopic(base, index))
	if err != nil || !held {
		return workItem{}, false, err
	}
	var item workItem
	if err := json.Unmarshal(payload, &item); err != nil || item.Path == "" {
		return workItem{}, false, fmt.Errorf("the item at index %d is not a video: %q", index, payload)
	}
	return item, true, nil
}

// Clears every video of a list and then its count, and returns once the
// broker has read every clear. The count goes last, so a clear that stops
// part of the way leaves the count for the next clear to find.
func clearWorkList(session *busSession, base string, list workList, count int) error {
	for index := range count {
		if err := session.retain(list.itemTopic(base, index), nil); err != nil {
			return err
		}
	}
	if err := session.retain(list.countTopic(base), nil); err != nil {
		return err
	}
	return session.flush()
}

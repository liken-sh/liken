package main

// progressstore.go is the progress store's client: the HTTP shape a
// Corrosion agent answers, spoken to the progress agent in the same
// pod. Writes post statements to the transactions endpoint and reads
// post one statement to the queries endpoint. Nothing writes the
// database outside the agent, because a write outside it corrupts the
// CRDT clocks.
//
// The client is the catalog's in shape and not in code, because the two
// speak to different agents and their failures name different stores.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"time"
)

// The phase a running Play carries in the store. The phases past it are
// the Play's own, from the final status the operator publishes.
const playPhaseRunning = "Running"

// progressStore writes to one Corrosion agent. base is the agent's API
// address, bound to loopback in the pod.
type progressStore struct {
	base string
	http *http.Client
}

// newProgressStore builds a client from the agent's base address and an
// HTTP client. A test hands in an httptest server's base and its
// client.
func newProgressStore(base string, httpClient *http.Client) *progressStore {
	return &progressStore{base: base, http: httpClient}
}

// playRow is one row of the plays table, as the store writes it and
// reads it back. The times are Unix seconds and the position and the
// duration are seconds, which is the shape the schema holds.
type playRow struct {
	Play     string
	Player   string
	Library  string
	Watch    string
	Started  int64
	Ended    int64
	Item     int
	Position int
	Duration int
	Phase    string
	Season   int
	Episode  int
	Recorded int64
}

// recordPosition writes where one Play reached. The insert stamps the
// time the row started and the update moves everything a report
// carries, so a report for a Play the store has not seen creates its
// row.
func (s *progressStore) recordPosition(ctx context.Context, play string, item, position, duration int, at time.Time) error {
	return s.apply(ctx, []statement{
		startPlay(play, at),
		{
			sql: `UPDATE plays SET item = ?, position = ?, duration = ?, phase = ?, recorded = ?` +
				` WHERE play = ?`,
			params: []any{item, position, duration, playPhaseRunning, at.Unix(), play},
		},
	})
}

// recordAudience writes what the operator knows about a Play: the
// Player it ran on, the Library the items came from, the Watch it
// belongs to, the episode, the people who watched, and the work's
// aliases.
//
// It leaves the recorded time alone, because the operator republishes
// the audience on every pass, and a bump there would make an idle Play
// read as the latest write of its Watch.
func (s *progressStore) recordAudience(ctx context.Context, play string, audience playAudience, at time.Time) error {
	statements := []statement{
		startPlay(play, at),
		{
			sql: `UPDATE plays SET player = ?, library = ?, watch = ?, season = ?, episode = ?` +
				` WHERE play = ?`,
			params: []any{audience.Player, audience.Library, audience.Watch,
				audience.Season, audience.Episode, play},
		},
		{sql: `DELETE FROM play_people WHERE play = ?`, params: []any{play}},
		{sql: `DELETE FROM play_aliases WHERE play = ?`, params: []any{play}},
	}
	for _, person := range audience.People {
		statements = append(statements, statement{
			sql:    `INSERT INTO play_people (play, person) VALUES (?, ?)`,
			params: []any{play, person},
		})
	}
	for _, provider := range slices.Sorted(maps.Keys(audience.Aliases)) {
		statements = append(statements, statement{
			sql:    `INSERT INTO play_aliases (play, provider, id) VALUES (?, ?, ?)`,
			params: []any{play, provider, audience.Aliases[provider]},
		})
	}
	return s.apply(ctx, statements)
}

// recordFinal writes the last status of a Play and marks the row ended.
// The operator reads that mark back off the bus before it releases the
// Play, so a Play is never deleted before its last position is here.
func (s *progressStore) recordFinal(ctx context.Context, play string, final playFinal, position, duration int, at time.Time) error {
	return s.apply(ctx, []statement{
		startPlay(play, at),
		{
			sql: `UPDATE plays SET phase = ?, item = ?, position = ?, duration = ?,` +
				` ended = ?, recorded = ? WHERE play = ?`,
			params: []any{final.Phase, final.Item, position, duration, at.Unix(), at.Unix(), play},
		},
	})
}

// startPlay creates one Play's row where there is none and stamps the
// time of that first write. A row that already stands is left alone, so
// every later write moves the columns it owns and no other.
func startPlay(play string, at time.Time) statement {
	return statement{
		sql:    `INSERT INTO plays (play, started, recorded) VALUES (?, ?, ?) ON CONFLICT (play) DO NOTHING`,
		params: []any{play, at.Unix(), at.Unix()},
	}
}

// forgetPerson takes one person out of every Play in this namespace's
// store. The Plays stay, because a Play with no people left is still a
// Player's row, and the people who remain in a shared Play stay with
// it.
func (s *progressStore) forgetPerson(ctx context.Context, person string) error {
	return s.apply(ctx, []statement{
		{sql: `DELETE FROM play_people WHERE person = ?`, params: []any{person}},
	})
}

// watchOf is the Watch one Play belongs to, or empty for a Play in no
// Watch and for a Play the store has no row for.
func (s *progressStore) watchOf(ctx context.Context, play string) (string, error) {
	cells, err := s.row(ctx, `SELECT watch FROM plays WHERE play = ? LIMIT 1`, []any{play})
	if err != nil || len(cells) == 0 {
		return "", err
	}
	text, _ := cells[0].(string)
	return text, nil
}

// latestForWatch is the last Play recorded against one Watch, which is
// the projection the operator writes into that Watch's status. It
// answers held false for a Watch nothing was recorded against.
func (s *progressStore) latestForWatch(ctx context.Context, watch string) (playRow, bool, error) {
	cells, err := s.row(ctx,
		`SELECT play, item, position, duration, season, episode, ended, recorded`+
			` FROM plays WHERE watch = ? ORDER BY recorded DESC LIMIT 1`, []any{watch})
	if err != nil || len(cells) < 8 {
		return playRow{}, false, err
	}
	play, _ := cells[0].(string)
	return playRow{
		Play:     play,
		Watch:    watch,
		Item:     cellInt(cells[1]),
		Position: cellInt(cells[2]),
		Duration: cellInt(cells[3]),
		Season:   cellInt(cells[4]),
		Episode:  cellInt(cells[5]),
		Ended:    int64(cellInt(cells[6])),
		Recorded: int64(cellInt(cells[7])),
	}, true, nil
}

// cellInt reads one cell of a streamed row as an integer. A SqliteValue
// integer arrives as a JSON number, which decodes to float64.
func cellInt(cell any) int {
	number, _ := cell.(float64)
	return int(number)
}

// apply posts one batch of statements. The batch is small by nature:
// one Play's audience is a row, its people, and its aliases, so there
// is no chunking here.
func (s *progressStore) apply(ctx context.Context, statements []statement) error {
	body := make([]any, len(statements))
	for at, held := range statements {
		params := held.params
		if params == nil {
			params = []any{}
		}
		body[at] = []any{held.sql, params}
	}
	// The payload is strings, numbers, and slices of them, so it always
	// marshals, and there is no failure here for a caller to answer.
	payload, _ := json.Marshal(body)

	resp, err := s.post(ctx, transactionsPath, payload)
	if err != nil {
		return err
	}
	defer drain(resp.Body)
	if err := progressFailure("write", resp); err != nil {
		return err
	}

	var result transactionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("the progress store: decoding the answer: %w", err)
	}
	// One result per statement is the contract, so a short answer is a
	// failure and never a batch that applied.
	if len(result.Results) != len(statements) {
		return fmt.Errorf("the progress store: %d results for %d statements",
			len(result.Results), len(statements))
	}
	for _, held := range result.Results {
		if held.Error != "" {
			return fmt.Errorf("the progress store: %s", held.Error)
		}
	}
	return nil
}

// row runs one read and answers the first row's cells, or nil where the
// query answered none. Every read this store makes names a LIMIT of
// one, so nothing here holds a result set.
func (s *progressStore) row(ctx context.Context, sql string, params []any) ([]any, error) {
	payload, _ := json.Marshal([]any{sql, params})

	resp, err := s.post(ctx, queriesPath, payload)
	if err != nil {
		return nil, err
	}
	defer drain(resp.Body)
	if err := progressFailure("read", resp); err != nil {
		return nil, err
	}

	var first []any
	// The endpoint answers a query as newline-delimited JSON events, so
	// the reader holds one event at a time.
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), queryReadLimit)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		cells, isError, message, err := decodeQueryEvent(line)
		if err != nil {
			return nil, err
		}
		if isError {
			return nil, fmt.Errorf("the progress store: %s", message)
		}
		if cells != nil && first == nil {
			first = cells
		}
	}
	return first, scanner.Err()
}

// post sends one JSON payload to one of the agent's endpoints.
func (s *progressStore) post(ctx context.Context, path string, payload []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return s.http.Do(req)
}

// progressFailure reads a non-2xx answer as the failure it is, with the
// first of the agent's own message.
func progressFailure(act string, resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		return nil
	}
	message, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return fmt.Errorf("the progress store %s: %s: %s", act, resp.Status, message)
}

package main

// `lab down` has to be infallible: if anything fails mid-way the laboratory
// must not stay alive. The properties that make it so:
//
//   * it lists by TAG from the API — the truth — never from the local state
//     file (which a crashed `up` may not have written);
//   * one droplet's failure never stops the others;
//   * every destroy is retried with backoff;
//   * the verdict comes from a FINAL RE-LISTING, not from the loop's own
//     bookkeeping — `down` succeeds only when the API says nothing is left;
//   * it is idempotent: re-running after an interruption finishes the job,
//     and the reaper is the backstop behind that.

import (
	"context"
	"fmt"
	"time"
)

// downAll destroys every lab-tagged droplet. It returns the droplets STILL
// ALIVE after the attempts (per the final re-listing) and an error when any
// survive. retries is per droplet; backoff separates attempts.
func downAll(ctx context.Context, c *Client, retries int, backoff time.Duration) ([]Droplet, error) {
	ds, err := c.ListLab(ctx)
	if err != nil {
		return nil, fmt.Errorf("down: cannot list the laboratory: %w", err)
	}
	if len(ds) == 0 {
		c.logf("down: no lab droplets exist")
		return nil, nil
	}
	for _, d := range ds {
		var last error
		for att := 0; att < retries; att++ {
			if att > 0 {
				time.Sleep(backoff)
			}
			if last = c.Destroy(ctx, d); last == nil {
				break
			}
			c.logf("down: destroy %s (id %d) attempt %d/%d failed: %v", d.Name, d.ID, att+1, retries, last)
		}
	}
	if !c.apply {
		// A dry-run destroys nothing; re-listing would "fail" forever.
		return nil, nil
	}
	// The verdict is the API's, not ours.
	time.Sleep(backoff)
	survivors, err := c.ListLab(ctx)
	if err != nil {
		return nil, fmt.Errorf("down: destroys issued but the verification listing failed — re-run `lab down`: %w", err)
	}
	if len(survivors) > 0 {
		names := ""
		for _, s := range survivors {
			names += fmt.Sprintf(" %s(id %d)", s.Name, s.ID)
		}
		return survivors, fmt.Errorf("down: %d droplet(s) STILL ALIVE after retries:%s — re-run `lab down`; `lab reap` is the backstop", len(survivors), names)
	}
	c.logf("down: verified — zero applab droplets remain")
	return nil, nil
}

// reap destroys every lab droplet older than maxAge and REPORTS (never
// destroys) anything the guard excludes. Returns what it reaped.
func reap(ctx context.Context, c *Client, maxAge time.Duration) ([]Droplet, error) {
	ds, err := c.ListLab(ctx)
	if err != nil {
		return nil, fmt.Errorf("reap: cannot list: %w", err)
	}
	old, skipped := ReapSelect(ds, maxAge, time.Now())
	for _, s := range skipped {
		c.logf("reap: SKIPPED %s (id %d): carries the tag but fails the guard — inspect it by hand", s.Name, s.ID)
	}
	for _, d := range old {
		if err := c.Destroy(ctx, d); err != nil {
			c.logf("reap: destroy %s (id %d) failed: %v", d.Name, d.ID, err)
		}
	}
	c.logf("reap: %d droplet(s) older than %s reaped, %d younger kept, %d skipped by guard",
		len(old), maxAge, len(ds)-len(old)-len(skipped), len(skipped))
	return old, nil
}

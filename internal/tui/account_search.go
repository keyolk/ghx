package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/pr"
)

type accountSearchResult struct {
	prs []pr.Summary
	err error
}

// searchPRsAcrossAccounts runs an unscoped source once per configured account.
// Results are merged in configuration order so a PR visible to multiple
// accounts keeps a deterministic credential for every later action.
func searchPRsAcrossAccounts(
	ctx context.Context,
	client *gh.Client,
	accounts []config.AccountDef,
	query string,
	limit int,
) ([]pr.Summary, error, error) {
	if len(accounts) == 0 {
		prs, err := client.SearchPRs(ctx, query, limit)
		return prs, nil, err
	}

	results := make([]accountSearchResult, len(accounts))
	var wg sync.WaitGroup
	for i, account := range accounts {
		i, account := i, account
		selector := account.Selector()
		if selector == "" {
			results[i].err = fmt.Errorf("account %q has no gh_user or credential_repo", account.Name)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i].prs, results[i].err = client.WithCredentialRepo(selector).SearchPRs(ctx, query, limit)
		}()
	}
	wg.Wait()

	seen := make(map[string]bool)
	var merged []pr.Summary
	var failures []error
	succeeded := 0
	for i, result := range results {
		if result.err != nil {
			failures = append(failures, fmt.Errorf("account %s: %w", accounts[i].Label(), result.err))
			continue
		}
		succeeded++
		for _, summary := range result.prs {
			key := summary.ID
			if key == "" {
				key = fmt.Sprintf("%s#%d", strings.ToLower(summary.Repo), summary.Number)
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, summary)
		}
	}
	if succeeded == 0 {
		return nil, nil, errors.Join(failures...)
	}

	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].UpdatedAt.After(merged[j].UpdatedAt)
	})
	return merged, errors.Join(failures...), nil
}

// Package bq provides a thin BigQuery client for writing EV history data to
// the card_market_history and set_market_history tables.
package bq

import (
	"context"
	"fmt"

	"cloud.google.com/go/bigquery"
	"cloud.google.com/go/civil"
	"google.golang.org/api/iterator"
)

// SetMarketRow is one row in the set_market_history table.
type SetMarketRow struct {
	Game      string     `bigquery:"game"`
	SetID     string     `bigquery:"set_id"`
	Month     civil.Date `bigquery:"month"`
	EV        float64    `bigquery:"ev"`
	SetValue  float64    `bigquery:"set_value"`
	Top5Value float64    `bigquery:"top_5_value"`
	Top5Ratio float64    `bigquery:"top_5_ratio"`
}

// CardMarketRow is one row in the card_market_history table.
type CardMarketRow struct {
	CardID      string               `bigquery:"card_id"`
	Month       civil.Date           `bigquery:"month"`
	GradeID     string               `bigquery:"grade_id"`
	MarketPrice bigquery.NullFloat64 `bigquery:"market_price"`
	Volume      bigquery.NullInt64   `bigquery:"volume"`
}

// TCGPlayerMarketRow is one row in the tcgplayer_market_snapshots table.
type TCGPlayerMarketRow struct {
	SnapshotDate          civil.Date           `bigquery:"snapshot_date"`
	TCG                   string               `bigquery:"tcg"`
	SetID                 string               `bigquery:"set_id"`
	ProductType           string               `bigquery:"product_type"`
	TCGPlayerID           string               `bigquery:"tcgplayer_id"`
	SellerCount           int                  `bigquery:"seller_count"`
	ProductCount          int                  `bigquery:"product_count"`
	MedianAskPrice        float64              `bigquery:"median_ask_price"`
	AvgSold30d            float64              `bigquery:"avg_sold_30d"`
	SalesToInventoryRatio bigquery.NullFloat64 `bigquery:"sales_to_inventory_ratio"`
}

// Client wraps a BigQuery client scoped to a project and dataset.
type Client struct {
	bq      *bigquery.Client
	dataset string
}

// NewClient creates a BQ client for the given project and dataset.
func NewClient(ctx context.Context, projectID, dataset string) (*Client, error) {
	client, err := bigquery.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("bigquery.NewClient: %w", err)
	}
	return &Client{bq: client, dataset: dataset}, nil
}

// Close releases the underlying BQ connection.
func (c *Client) Close() error {
	return c.bq.Close()
}

// ExistingSetMonths returns the set of "set_id|YYYY-MM-01" keys already
// present in the given table. Used to skip months that have already been
// inserted so the job is safe to re-run.
func (c *Client) ExistingSetMonths(ctx context.Context, table string) (map[string]bool, error) {
	q := c.bq.Query(fmt.Sprintf(
		"SELECT set_id, CAST(month AS STRING) AS month FROM `%s.%s` WHERE month >= '2000-01-01'",
		c.dataset, table,
	))
	it, err := q.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("querying existing months from %s: %w", table, err)
	}
	existing := make(map[string]bool)
	for {
		var row struct {
			SetID string `bigquery:"set_id"`
			Month string `bigquery:"month"`
		}
		if err := it.Next(&row); err == iterator.Done {
			break
		} else if err != nil {
			return nil, fmt.Errorf("iterating existing months: %w", err)
		}
		existing[row.SetID+"|"+row.Month] = true
	}
	return existing, nil
}

// ExistingCardMonthPairs returns the set of "card_id|YYYY-MM-01" keys already
// present in the given card_market_history table for the given game and set.
// card_id format: "game_setID_cardNumber"
func (c *Client) ExistingCardMonthPairs(ctx context.Context, table, game, setID string) (map[string]bool, error) {
	prefix := game + "_" + setID + "_"
	q := c.bq.Query(fmt.Sprintf(
		"SELECT DISTINCT card_id, CAST(month AS STRING) AS month FROM `%s.%s` WHERE month >= '2000-01-01' AND card_id LIKE @prefix",
		c.dataset, table,
	))
	q.Parameters = []bigquery.QueryParameter{
		{Name: "prefix", Value: prefix + "%"},
	}
	it, err := q.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("querying existing card month pairs from %s: %w", table, err)
	}
	existing := make(map[string]bool)
	for {
		var row struct {
			CardID string `bigquery:"card_id"`
			Month  string `bigquery:"month"`
		}
		if err := it.Next(&row); err == iterator.Done {
			break
		} else if err != nil {
			return nil, fmt.Errorf("iterating existing card month pairs: %w", err)
		}
		existing[row.CardID+"|"+row.Month] = true
	}
	return existing, nil
}

const batchSize = 500

// InsertSetRows streams rows into set_market_history in batches.
func (c *Client) InsertSetRows(ctx context.Context, table string, rows []SetMarketRow) error {
	ins := c.bq.Dataset(c.dataset).Table(table).Inserter()
	for i := 0; i < len(rows); i += batchSize {
		end := i + batchSize
		if end > len(rows) {
			end = len(rows)
		}
		if err := ins.Put(ctx, rows[i:end]); err != nil {
			return fmt.Errorf("inserting set rows [%d:%d]: %w", i, end, err)
		}
	}
	return nil
}

// InsertCardRows streams rows into card_market_history in batches.
func (c *Client) InsertCardRows(ctx context.Context, table string, rows []CardMarketRow) error {
	ins := c.bq.Dataset(c.dataset).Table(table).Inserter()
	for i := 0; i < len(rows); i += batchSize {
		end := i + batchSize
		if end > len(rows) {
			end = len(rows)
		}
		if err := ins.Put(ctx, rows[i:end]); err != nil {
			return fmt.Errorf("inserting card rows [%d:%d]: %w", i, end, err)
		}
	}
	return nil
}

// ExistingMarketSnapshotKeys returns "tcg|set_id|product_type" keys already
// inserted for today's date. Used to skip products already scraped today.
func (c *Client) ExistingMarketSnapshotKeys(ctx context.Context, table string) (map[string]bool, error) {
	q := c.bq.Query(fmt.Sprintf(
		"SELECT tcg, set_id, product_type FROM `%s.%s` WHERE snapshot_date = CURRENT_DATE()",
		c.dataset, table,
	))
	it, err := q.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("querying existing market snapshots from %s: %w", table, err)
	}
	existing := make(map[string]bool)
	for {
		var row struct {
			TCG         string `bigquery:"tcg"`
			SetID       string `bigquery:"set_id"`
			ProductType string `bigquery:"product_type"`
		}
		if err := it.Next(&row); err == iterator.Done {
			break
		} else if err != nil {
			return nil, fmt.Errorf("iterating existing market snapshots: %w", err)
		}
		existing[row.TCG+"|"+row.SetID+"|"+row.ProductType] = true
	}
	return existing, nil
}

// InsertMarketSnapshotRows streams rows into tcgplayer_market_snapshots in batches.
func (c *Client) InsertMarketSnapshotRows(ctx context.Context, table string, rows []TCGPlayerMarketRow) error {
	ins := c.bq.Dataset(c.dataset).Table(table).Inserter()
	for i := 0; i < len(rows); i += batchSize {
		end := i + batchSize
		if end > len(rows) {
			end = len(rows)
		}
		if err := ins.Put(ctx, rows[i:end]); err != nil {
			return fmt.Errorf("inserting market snapshot rows [%d:%d]: %w", i, end, err)
		}
	}
	return nil
}

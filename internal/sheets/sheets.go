package sheets

import (
	"context"
	"fmt"

	"google.golang.org/api/option"
	googlesheets "google.golang.org/api/sheets/v4"
)

type Client struct {
	svc           *googlesheets.Service
	spreadsheetID string
}

// NewClient creates a Sheets client authenticated via a service account credentials file.
// The service account must be granted editor access to the target spreadsheet.
func NewClient(ctx context.Context, credentialsFile, spreadsheetID string) (*Client, error) {
	svc, err := googlesheets.NewService(ctx, option.WithCredentialsFile(credentialsFile))
	if err != nil {
		return nil, fmt.Errorf("creating sheets service: %w", err)
	}
	return &Client{svc: svc, spreadsheetID: spreadsheetID}, nil
}

// UpdateHeader overwrites row 1 with the given column names.
func (c *Client) UpdateHeader(sheetName string, headers []interface{}) error {
	vr := &googlesheets.ValueRange{Values: [][]interface{}{headers}}
	_, err := c.svc.Spreadsheets.Values.
		Update(c.spreadsheetID, fmt.Sprintf("%s!A1", sheetName), vr).
		ValueInputOption("RAW").
		Do()
	if err != nil {
		return fmt.Errorf("updating header in %q: %w", sheetName, err)
	}
	return nil
}

// ReadRows returns all data rows (A2 onwards) from the given sheet as string slices.
func (c *Client) ReadRows(sheetName string) ([][]string, error) {
	resp, err := c.svc.Spreadsheets.Values.
		Get(c.spreadsheetID, sheetName+"!A2:Z").
		Do()
	if err != nil {
		return nil, fmt.Errorf("reading rows from %q: %w", sheetName, err)
	}
	result := make([][]string, len(resp.Values))
	for i, row := range resp.Values {
		strs := make([]string, len(row))
		for j, v := range row {
			strs[j] = fmt.Sprint(v)
		}
		result[i] = strs
	}
	return result, nil
}

// AppendRows appends rows after the last existing data row in the sheet.
func (c *Client) AppendRows(sheetName string, rows [][]interface{}) error {
	if len(rows) == 0 {
		return nil
	}
	vr := &googlesheets.ValueRange{Values: rows}
	_, err := c.svc.Spreadsheets.Values.
		Append(c.spreadsheetID, sheetName+"!A1", vr).
		ValueInputOption("USER_ENTERED").
		InsertDataOption("INSERT_ROWS").
		Do()
	if err != nil {
		return fmt.Errorf("appending rows to %q: %w", sheetName, err)
	}
	return nil
}

// WriteAllRows clears all data rows (A2:Z) then writes the given rows starting
// at A2 in a single API call, preserving the header row.
func (c *Client) WriteAllRows(sheetName string, rows [][]interface{}) error {
	if _, err := c.svc.Spreadsheets.Values.
		Clear(c.spreadsheetID, sheetName+"!A2:Z", &googlesheets.ClearValuesRequest{}).
		Do(); err != nil {
		return fmt.Errorf("clearing rows in %q: %w", sheetName, err)
	}

	if len(rows) == 0 {
		return nil
	}

	vr := &googlesheets.ValueRange{
		Values: rows,
	}
	if _, err := c.svc.Spreadsheets.Values.
		Update(c.spreadsheetID, fmt.Sprintf("%s!A2", sheetName), vr).
		ValueInputOption("USER_ENTERED").
		Do(); err != nil {
		return fmt.Errorf("writing rows to %q: %w", sheetName, err)
	}
	return nil
}

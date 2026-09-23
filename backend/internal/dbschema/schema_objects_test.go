package dbschema

import (
	"context"
	"testing"
)

func findTable(set ObjectSet, name string) (TableObjects, bool) {
	for _, table := range set.Tables {
		if table.Name == name {
			return table, true
		}
	}
	return TableObjects{}, false
}

func violationKinds(violations []Violation) []string {
	kinds := make([]string, 0, len(violations))
	for _, v := range violations {
		kinds = append(kinds, v.Kind)
	}
	return kinds
}

func TestCompareObjectsDetectsDriftAndAllowsExtras(t *testing.T) {
	reference := ObjectSet{Tables: []TableObjects{
		{
			Name: "accounts",
			Columns: []ColumnObjects{
				{Name: "id", Type: "integer"},
				{Name: "name", Type: "text", Nullable: true},
			},
			PKColumns: []string{"id"},
			Indexes:   []IndexObjects{{Columns: []string{"name"}, Unique: true}},
			Triggers:  []string{"accounts_guard"},
		},
	}}
	actual := ObjectSet{Tables: []TableObjects{
		{
			Name: "accounts",
			Columns: []ColumnObjects{
				{Name: "id", Type: "integer"},
				// Type and nullability drift on the same column.
				{Name: "name", Type: "blob", Nullable: true},
				// Extra columns from earlier releases are allowed.
				{Name: "legacy_team", Type: "text", Nullable: true},
			},
			PKColumns: []string{"legacy_team"},
			Indexes:   []IndexObjects{{Columns: []string{"legacy_team"}, Unique: false}},
			Triggers:  []string{"accounts_other"},
		},
		// Extra tables are allowed.
		{Name: "legacy_extra", Columns: []ColumnObjects{{Name: "id", Type: "integer"}}},
	}}
	violations := CompareObjects(reference, actual)
	kinds := violationKinds(violations)
	expectedKinds := []string{"column_type_mismatch", "missing_index", "missing_trigger", "primary_key_mismatch"}
	if len(kinds) != len(expectedKinds) {
		t.Fatalf("expected violations %v, got %v", expectedKinds, kinds)
	}
	for _, want := range expectedKinds {
		found := false
		for _, kind := range kinds {
			if kind == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected violation kind %s in %v", want, kinds)
		}
	}

	missing := ObjectSet{Tables: []TableObjects{
		{Name: "accounts", Columns: []ColumnObjects{
			{Name: "id", Type: "integer"},
			{Name: "email", Type: "text"},
		}},
	}}
	violations = CompareObjects(missing, ObjectSet{})
	kinds = violationKinds(violations)
	if len(kinds) != 1 || kinds[0] != "missing_table" {
		t.Fatalf("expected missing_table only, got %v", kinds)
	}
}

func TestIntrospectSQLiteCapturesShape(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	statements := []string{
		"CREATE TABLE accounts (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, email TEXT)",
		"CREATE UNIQUE INDEX accounts_email_key ON accounts (email)",
		"CREATE INDEX accounts_name_idx ON accounts (name)",
		"CREATE TRIGGER accounts_guard AFTER INSERT ON accounts BEGIN SELECT 1; END",
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("setup %q: %v", statement, err)
		}
	}
	// Expression indexes and partial-expression shapes must not break introspection.
	if _, err := db.Exec("CREATE TABLE notes (body TEXT, id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE INDEX notes_expr ON notes (lower(body))"); err != nil {
		t.Fatal(err)
	}
	set, err := Introspect(ctx, db, DialectSQLite, "")
	if err != nil {
		t.Fatalf("Introspect: %v", err)
	}
	accounts, ok := findTable(set, "accounts")
	if !ok {
		t.Fatal("accounts table not introspected")
	}
	columns := map[string]ColumnObjects{}
	for _, column := range accounts.Columns {
		columns[column.Name] = column
	}
	if id := columns["id"]; id.Type != "integer" || id.Nullable {
		t.Fatalf("unexpected id column: %+v", id)
	}
	if name := columns["name"]; name.Type != "text" || name.Nullable {
		t.Fatalf("unexpected name column: %+v", name)
	}
	if len(accounts.PKColumns) != 1 || accounts.PKColumns[0] != "id" {
		t.Fatalf("unexpected primary key: %v", accounts.PKColumns)
	}
	uniqueByName := false
	plainByName := false
	for _, index := range accounts.Indexes {
		if len(index.Columns) == 1 && index.Columns[0] == "name" {
			plainByName = !index.Unique
		}
		if len(index.Columns) == 1 && index.Columns[0] == "email" {
			uniqueByName = index.Unique
		}
	}
	if !uniqueByName || !plainByName {
		t.Fatalf("expected unique email and plain name indexes, got %+v", accounts.Indexes)
	}
	if len(accounts.Triggers) != 1 || accounts.Triggers[0] != "accounts_guard" {
		t.Fatalf("unexpected triggers: %v", accounts.Triggers)
	}
	notes, ok := findTable(set, "notes")
	if !ok {
		t.Fatal("notes table not introspected")
	}
	if len(notes.Indexes) != 0 {
		t.Fatalf("expression-only indexes must be skipped, got %+v", notes.Indexes)
	}
}

func TestAdoptionReferenceVerificationRefusesDrift(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec("CREATE TABLE accounts (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	driftedReference := func(context.Context) (ObjectSet, error) {
		return ObjectSet{Tables: []TableObjects{{
			Name: "accounts",
			Columns: []ColumnObjects{
				{Name: "id", Type: "integer"},
				{Name: "name", Type: "blob"},
			},
			PKColumns: []string{"id"},
		}}}, nil
	}
	runner := mustRunner(t, db, nil, WithAdoptionReference(driftedReference))
	_, err := runner.Adopt(ctx, nil)
	requireErrorCode(t, err, ErrCodeSchemaVerification)
	status, err := runner.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.BaselineRecorded {
		t.Fatal("baseline must not be recorded when verification fails")
	}
	if outcome := lastAttemptOutcome(t, db, BaselineVersion); outcome != "failed" {
		t.Fatalf("expected failed adoption attempt, got %q", outcome)
	}
}

func TestAdoptionReferenceHappyPathRecordsBaseline(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec("CREATE TABLE accounts (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, legacy_extra TEXT)"); err != nil {
		t.Fatal(err)
	}
	matchingReference := func(context.Context) (ObjectSet, error) {
		return ObjectSet{Tables: []TableObjects{{
			Name: "accounts",
			Columns: []ColumnObjects{
				{Name: "id", Type: "integer"},
				{Name: "name", Type: "text"},
			},
			PKColumns: []string{"id"},
		}}}, nil
	}
	runner := mustRunner(t, db, nil, WithAdoptionReference(matchingReference))
	result, err := runner.Adopt(ctx, nil)
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if !result.Adopted {
		t.Fatal("expected baseline recorded with a matching reference")
	}
}

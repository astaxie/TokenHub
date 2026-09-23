package dbschema

// isHistoricalMeteringChecksum recognizes the one override accidentally shipped
// for version 4 in 570668207 and retained in bd26c7d16 after the original SQL
// was restored. The original SQL checksum comes from fdb197340. Version 5
// separately supplies the audit correlation expansion from the intermediate
// implementation. Never rewrite the ledger or accept this override for changed
// SQL, another migration, or a dirty row.
func isHistoricalMeteringChecksum(m Migration, row Applied) bool {
	const originalChecksum = "4d282c33fb83adcf772a560ddb2b772116e9f6bbd3ecee0a68bda70fc447e50c"
	return m.Version == 4 && row.Version == 4 &&
		m.Name == "add-metering-evidence" && row.Name == m.Name &&
		m.Phase == PhaseExpand && row.Phase == PhaseExpand &&
		m.Dialect == "" && m.Go == nil && m.ChecksumOverride == "" &&
		m.Checksum() == originalChecksum && !row.Dirty &&
		row.Checksum == "tokenhub-schema-metering-evidence-v2"
}

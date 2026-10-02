package server

// ReadSQLiteSecretKeySidecar returns the secret key persisted beside a
// file-backed SQLite database, when one exists. It is strictly read-only:
// provisioning or creating a sidecar key stays the startup flow's
// responsibility, so maintenance tooling can resolve the key without any
// side effect on the deployment.
func ReadSQLiteSecretKeySidecar(databaseURL string) (string, bool, error) {
	keyPath, _, ok, err := sqliteSecretKeyPaths(databaseURL)
	if err != nil || !ok {
		return "", false, err
	}
	return readSQLiteSecretKey(keyPath)
}

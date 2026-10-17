package server

func sanitizeMediaError(protocol string, err error) error {
	if mediaCapabilityForProtocol(protocol) == AdapterCapabilityTranscription {
		return sanitizeTranscriptionProviderError(err)
	}
	return err
}

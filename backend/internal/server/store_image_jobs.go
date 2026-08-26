package server

func (s *GormStore) UpdateImageJobRequest(job ImageJob, prompt string) error {
	updates := map[string]any{
		"model":             job.Model,
		"quality":           job.Quality,
		"size":              job.Size,
		"prompt_ciphertext": s.encryptSecret(prompt),
	}
	return s.db.Model(&ImageJob{}).Where("id = ?", job.ID).Updates(updates).Error
}

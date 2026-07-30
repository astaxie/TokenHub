package server

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
)

func (s *GormStore) CreateImageJob(job ImageJob, prompt string) (ImageJob, error) {
	if strings.TrimSpace(job.ID) == "" {
		job.ID = NewID("imgjob")
	}
	if strings.TrimSpace(job.Status) == "" {
		job.Status = "queued"
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	promptCiphertext, err := s.encryptSecret(prompt)
	if err != nil {
		return ImageJob{}, fmt.Errorf("encrypt prompt for image job %s: %w", job.ID, err)
	}
	job.PromptCiphertext = promptCiphertext
	job.Prompt = prompt
	if err := s.db.Create(&job).Error; err != nil {
		return ImageJob{}, err
	}
	return job, nil
}

func (s *GormStore) ClaimImageJob(id string) (ImageJob, bool, error) {
	now := time.Now().UTC()
	result := s.db.Model(&ImageJob{}).
		Where("id = ? AND status = ?", id, "queued").
		Updates(map[string]any{"status": "running", "started_at": now})
	if result.Error != nil {
		return ImageJob{}, false, result.Error
	}
	if result.RowsAffected == 0 {
		return ImageJob{}, false, nil
	}
	var job ImageJob
	if err := s.db.First(&job, "id = ?", id).Error; err != nil {
		return ImageJob{}, false, err
	}
	if err := s.decryptImageJobPrompts(&job); err != nil {
		return ImageJob{}, false, err
	}
	return job, true, nil
}

func (s *GormStore) GetImageJob(id string) (ImageJob, bool) {
	var job ImageJob
	if err := s.db.First(&job, "id = ?", id).Error; err != nil {
		return ImageJob{}, false
	}
	if err := s.decryptImageJobPrompts(&job); err != nil {
		log.Printf("[tokenhub] ERROR: GetImageJob failed to decrypt prompts for job %s: %v", job.ID, err)
	}
	return job, true
}

func (s *GormStore) ListImageJobs(limit int) []ImageJob {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var jobs []ImageJob
	if err := s.db.Order("created_at desc").Limit(limit).Find(&jobs).Error; err != nil {
		return nil
	}
	for index := range jobs {
		if err := s.decryptImageJobPrompts(&jobs[index]); err != nil {
			log.Printf("[tokenhub] ERROR: ListImageJobs failed to decrypt prompts for job %s: %v", jobs[index].ID, err)
		}
	}
	return jobs
}

func (s *GormStore) FailUnfinishedImageJobs(code string, message string) ([]ImageJob, error) {
	now := time.Now().UTC()
	var jobs []ImageJob
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("status IN ?", []string{"queued", "running"}).Find(&jobs).Error; err != nil {
			return err
		}
		if len(jobs) == 0 {
			return nil
		}
		if err := tx.Model(&ImageJob{}).
			Where("status IN ?", []string{"queued", "running"}).
			Updates(map[string]any{
				"status":        "failed",
				"error_code":    code,
				"error_message": message,
				"completed_at":  now,
			}).Error; err != nil {
			return err
		}
		for _, job := range jobs {
			if strings.TrimSpace(job.RequestID) == "" {
				continue
			}
			if err := tx.Delete(&InFlightLease{}, "id = ?", job.RequestID).Error; err != nil {
				return err
			}
			var count int64
			if err := tx.Model(&RequestLog{}).Where("request_id = ?", job.RequestID).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				if err := tx.Create(&RequestLog{
					ID:         NewID("log"),
					RequestID:  job.RequestID,
					ProjectID:  job.ProjectID,
					APIKeyID:   job.APIKeyID,
					ModelName:  job.Model,
					StatusCode: http.StatusServiceUnavailable,
					ErrorCode:  code,
					LatencyMS:  now.Sub(job.CreatedAt).Milliseconds(),
					CreatedAt:  now,
				}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for index := range jobs {
		jobs[index].Status = "failed"
		jobs[index].ErrorCode = code
		jobs[index].ErrorMessage = message
		jobs[index].CompletedAt = &now
		if err := s.decryptImageJobPrompts(&jobs[index]); err != nil {
			log.Printf("[tokenhub] ERROR: FailUnfinishedImageJobs failed to decrypt prompts for job %s: %v", jobs[index].ID, err)
		}
	}
	return jobs, nil
}

func (s *GormStore) UpdateImageJob(job ImageJob, revisedPrompt string) error {
	if strings.TrimSpace(revisedPrompt) != "" {
		revisedPromptCiphertext, err := s.encryptSecret(revisedPrompt)
		if err != nil {
			return fmt.Errorf("encrypt revised prompt for image job %s: %w", job.ID, err)
		}
		job.RevisedPromptCiphertext = revisedPromptCiphertext
		job.RevisedPrompt = revisedPrompt
	}
	return s.db.Save(&job).Error
}

func (s *GormStore) CompleteImageJob(call CallContext, job ImageJob, revisedPrompt string, asset ImageAsset, route RouteSelection, usage Usage, clientIP string, userAgent string) error {
	elapsed := time.Duration(0)
	if !call.StartedAt.IsZero() {
		elapsed = time.Since(call.StartedAt)
	}
	usage = priceUsage(call.Model, usage)

	now := time.Now().UTC()
	if job.CompletedAt == nil {
		job.CompletedAt = &now
	}
	if strings.TrimSpace(asset.ID) == "" {
		asset.ID = NewID("asset")
	}
	if asset.CreatedAt.IsZero() {
		asset.CreatedAt = now
	}
	revisedPromptCiphertext := job.RevisedPromptCiphertext
	if strings.TrimSpace(revisedPrompt) != "" {
		encrypted, err := s.encryptSecret(revisedPrompt)
		if err != nil {
			return fmt.Errorf("encrypt revised prompt for image job %s: %w", job.ID, err)
		}
		revisedPromptCiphertext = encrypted
	}
	_ = s.stopInFlightLeaseHeartbeat(call.RequestID)

	err := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.db.Transaction(func(tx *gorm.DB) error {
			if err := s.finishCallTransaction(tx, call, route, usage, http.StatusOK, "", clientIP, userAgent, now); err != nil {
				return err
			}
			if err := tx.Create(&asset).Error; err != nil {
				return err
			}
			result := tx.Model(&ImageJob{}).
				Where("id = ? AND status = ?", job.ID, "running").
				Updates(map[string]any{
					"status":                    "completed",
					"provider_id":               job.ProviderID,
					"provider_resource_id":      job.ProviderResourceID,
					"provider_model":            job.ProviderModel,
					"upstream_request_id":       job.UpstreamRequestID,
					"input_tokens":              job.InputTokens,
					"cached_input_tokens":       job.CachedInputTokens,
					"output_tokens":             job.OutputTokens,
					"total_tokens":              job.TotalTokens,
					"revised_prompt_ciphertext": revisedPromptCiphertext,
					"error_code":                "",
					"error_message":             "",
					"completed_at":              job.CompletedAt,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("image job %s is not running", job.ID)
			}
			if route.Route.ID != "" {
				if err := tx.Model(&ModelRoute{}).Where("id = ?", route.Route.ID).Update("last_used_at", now).Error; err != nil {
					return err
				}
			}
			if resourceID := routeResourceID(route); resourceID != "" {
				if err := tx.Model(&ProviderResource{}).Where("id = ?", resourceID).
					Updates(map[string]any{"last_used_at": now, "updated_at": now}).Error; err != nil {
					return err
				}
			}
			return nil
		})
	}()
	if err != nil {
		if releaseErr := s.db.Delete(&InFlightLease{}, "id = ?", call.RequestID).Error; releaseErr != nil {
			log.Printf("[tokenhub] failed to release API key concurrency lease request=%s: %v", call.RequestID, releaseErr)
		}
	} else {
		s.observeGatewayCall(call, route, usage, http.StatusOK, "", elapsed)
	}
	return err
}

func (s *GormStore) decryptImageJobPrompts(job *ImageJob) error {
	prompt, err := s.decryptSecret(job.PromptCiphertext)
	if err != nil {
		return fmt.Errorf("decrypt prompt for image job %s: %w", job.ID, err)
	}
	revisedPrompt, err := s.decryptSecret(job.RevisedPromptCiphertext)
	if err != nil {
		return fmt.Errorf("decrypt revised prompt for image job %s: %w", job.ID, err)
	}
	job.Prompt = prompt
	job.RevisedPrompt = revisedPrompt
	return nil
}

func (s *GormStore) CreateImageAsset(asset ImageAsset) (ImageAsset, error) {
	if strings.TrimSpace(asset.ID) == "" {
		asset.ID = NewID("asset")
	}
	if asset.CreatedAt.IsZero() {
		asset.CreatedAt = time.Now().UTC()
	}
	if err := s.db.Create(&asset).Error; err != nil {
		return ImageAsset{}, err
	}
	return asset, nil
}

func (s *GormStore) ListImageAssets(jobID string) []ImageAsset {
	var assets []ImageAsset
	_ = s.db.Where("job_id = ?", jobID).Order("created_at asc").Find(&assets).Error
	return assets
}

func (s *GormStore) GetImageAsset(id string) (ImageAsset, bool) {
	var asset ImageAsset
	if err := s.db.First(&asset, "id = ?", id).Error; err != nil {
		return ImageAsset{}, false
	}
	return asset, true
}

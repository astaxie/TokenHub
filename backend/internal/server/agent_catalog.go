package server

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type agentCatalogFile struct {
	Agents []AgentRegistration `yaml:"agents"`
}

func (s *Server) syncAgentCatalog() {
	path := strings.TrimSpace(s.config.AgentCatalogFile)
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		log.Printf("[tokenhub] failed to read Agent catalog: %v", err)
		return
	}
	var catalog agentCatalogFile
	if err := yaml.Unmarshal(data, &catalog); err != nil {
		log.Printf("[tokenhub] failed to parse Agent catalog: %v", err)
		return
	}
	err = s.store.RunClusterOperation(context.Background(), "sync_agent_catalog", func(ctx context.Context) error {
		activeSlugs := make(map[string]bool, len(catalog.Agents))
		for _, registration := range catalog.Agents {
			slug := strings.ToLower(strings.TrimSpace(registration.Slug))
			if activeSlugs[slug] {
				return errors.New("Agent catalog contains a duplicate slug: " + slug)
			}
			activeSlugs[slug] = true
		}
		for _, registration := range catalog.Agents {
			registration.Status = normalizeAgentCatalogStatus(registration.Status)
			if _, err := s.registerAgent(ctx, registration, agentSourceConfig, "config"); err != nil {
				return err
			}
		}
		existing, err := s.store.ListAgents()
		if err != nil {
			return err
		}
		for _, agent := range existing {
			if agent.Source != agentSourceConfig || activeSlugs[agent.Slug] {
				continue
			}
			if agent.Status == StatusDisabled {
				continue
			}
			agent.Status = StatusDisabled
			instance := AgentInstance{}
			if len(agent.Instances) > 0 {
				instance = agent.Instances[0]
			}
			if _, err := s.store.SaveAgent(agent.Agent, agent.Card, instance, agent.Skills, "config"); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("[tokenhub] failed to synchronize Agent catalog: %v", err)
	}
}

func normalizeAgentCatalogStatus(status string) string {
	if strings.TrimSpace(status) == "" {
		return StatusActive
	}
	return status
}

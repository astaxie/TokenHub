package server

import pluginmeta "tokenhub/backend/internal/plugin"

type builtinActionRegistrar interface {
	Register(pluginmeta.ActionDescriptor, pluginmeta.ActionHandler) error
}

type builtinBackgroundJobRegistrar interface {
	Register(pluginmeta.BackgroundJobDescriptor, pluginmeta.BackgroundJobHandler) error
}

type enabledBuiltinActions struct {
	plugins *pluginmeta.Registry
	actions *pluginmeta.ActionBroker
}

func (r enabledBuiltinActions) Register(descriptor pluginmeta.ActionDescriptor, handler pluginmeta.ActionHandler) error {
	if !pluginHandlersEnabled(r.plugins, descriptor.PluginID) {
		return nil
	}
	return r.actions.Register(descriptor, handler)
}

type enabledBuiltinBackgroundJobs struct {
	plugins *pluginmeta.Registry
	jobs    *pluginmeta.BackgroundJobBroker
}

func (r enabledBuiltinBackgroundJobs) Register(descriptor pluginmeta.BackgroundJobDescriptor, handler pluginmeta.BackgroundJobHandler) error {
	if !pluginHandlersEnabled(r.plugins, descriptor.PluginID) {
		return nil
	}
	return r.jobs.Register(descriptor, handler)
}

func pluginHandlersEnabled(plugins *pluginmeta.Registry, pluginID string) bool {
	descriptor, found := plugins.Describe(pluginID)
	return found && (pluginmeta.PackageState{Status: descriptor.Status}).Loadable()
}

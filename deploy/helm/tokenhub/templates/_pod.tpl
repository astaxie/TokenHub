{{/*
Pod template partials, split by concern so each block stays readable:

- tokenhub.podMetadata    labels and annotations of the pod template
- tokenhub.podSpec        scheduling, security, and the container list
- tokenhub.container      the single TokenHub container
- tokenhub.probes         startup/readiness/liveness probes
- tokenhub.env            environment entries (plain values and secret refs)
- tokenhub.volumes        pod volumes (plugins emptyDir/PVC plus extras)
- tokenhub.volumeMounts   container volume mounts

Each partial emits content relative to the indentation its caller applies with
nindent.
*/}}

{{- define "tokenhub.podMetadata" -}}
labels:
  {{- include "tokenhub.selectorLabels" . | nindent 2 }}
annotations:
  # Rolls the pods whenever a chart-managed secret changes: Kubernetes does
  # not refresh env vars from updated Secrets, so a new database password or
  # secretEnv value only takes effect through a rollout. Externally managed
  # secrets (credentialsSecret, externalSecret) rotate outside this checksum;
  # restart the workload after changing them.
  checksum/secrets: {{ include (print $.Template.BasePath "/secret.yaml") . | sha256sum }}
{{- end }}

{{- define "tokenhub.podSpec" -}}
{{- with .Values.imagePullSecrets }}
imagePullSecrets:
  {{- toYaml . | nindent 2 }}
{{- end }}
serviceAccountName: {{ include "tokenhub.serviceAccountName" . }}
# The workload needs no Kubernetes API access.
automountServiceAccountToken: false
# Matches the image's node user (uid/gid 1000): the entrypoint skips its root
# bootstrap and fsGroup gives the group ownership of the plugins emptyDir.
securityContext:
  runAsNonRoot: true
  runAsUser: 1000
  runAsGroup: 1000
  fsGroup: 1000
# Covers the backend's 150s graceful shutdown window for in-flight streams.
terminationGracePeriodSeconds: 180
containers:
  - name: tokenhub
    {{- include "tokenhub.container" . | nindent 4 }}
{{ include "tokenhub.volumes" . }}
{{- with .Values.nodeSelector }}
nodeSelector:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .Values.affinity }}
affinity:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .Values.tolerations }}
tolerations:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- end }}

{{- define "tokenhub.container" -}}
image: "{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}"
imagePullPolicy: {{ .Values.image.pullPolicy }}
# The entrypoint materializes the release bundle from the image and then runs
# both processes via tokenhub-run.
securityContext:
  allowPrivilegeEscalation: false
  capabilities:
    drop:
      - ALL
ports:
  - name: api
    containerPort: 8080
    protocol: TCP
  - name: console
    containerPort: 3000
    protocol: TCP
{{- with .Values.resources }}
resources:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{ include "tokenhub.probes" . }}
env:
  {{- include "tokenhub.env" . | nindent 2 }}
volumeMounts:
  {{- include "tokenhub.volumeMounts" . | nindent 2 }}
{{- end }}

{{- define "tokenhub.probes" -}}
# Readiness keys off the API /readyz endpoint so pods stop receiving traffic
# when the database is unreachable. Liveness checks /livez; console process
# death is covered by tokenhub-run, which exits the container when either
# process dies.
startupProbe:
  httpGet:
    path: /readyz
    port: api
  periodSeconds: 10
  timeoutSeconds: 5
  failureThreshold: 30
readinessProbe:
  httpGet:
    path: /readyz
    port: api
  periodSeconds: 10
  timeoutSeconds: 5
  failureThreshold: 3
livenessProbe:
  httpGet:
    path: /livez
    port: api
  periodSeconds: 15
  timeoutSeconds: 3
  failureThreshold: 6
{{- end }}

{{- define "tokenhub.env" -}}
{{- $authSecret := default (include "tokenhub.secretName" .) .Values.credentialsSecret }}
{{- $apiBaseURL := include "tokenhub.apiBaseURL" . }}
- name: TOKENHUB_ENV
  value: {{ .Values.environment | quote }}
{{- if $apiBaseURL }}
- name: TOKENHUB_API_BASE_URL
  value: {{ $apiBaseURL | quote }}
{{- end }}
{{- if ne .Values.imageStorage.type "ephemeral" }}
- name: TOKENHUB_IMAGE_STORAGE_DIR
  value: {{ .Values.imageStorage.mountPath | quote }}
{{- end }}
{{- with .Values.extraEnv }}
{{- toYaml . | nindent 0 }}
{{- end }}
{{- /* Secret references: one env entry per secretEnv key. The database URL
resolves through its own secret so an existing credentialsSecret can combine
with any supported database source. */}}
{{- range $key, $value := .Values.secretEnv }}
- name: {{ $key }}
  valueFrom:
    secretKeyRef:
      {{- if eq $key "TOKENHUB_DATABASE_URL" }}
      name: {{ include "tokenhub.databaseSecretName" $ }}
      {{- else }}
      name: {{ $authSecret }}
      {{- end }}
      key: {{ $key }}
{{- end }}
{{- end }}

{{- define "tokenhub.volumeMounts" -}}
- name: plugins
  mountPath: /app/plugins
{{- if ne .Values.imageStorage.type "ephemeral" }}
- name: images
  mountPath: {{ .Values.imageStorage.mountPath }}
{{- end }}
{{ end }}

{{- define "tokenhub.volumes" -}}
volumes:
  # Plugin packages are ephemeral: the volume starts empty on every pod and
  # built-in plugins ship inside the image.
  - name: plugins
    emptyDir: {}
  {{- if eq .Values.imageStorage.type "pvc" }}
  - name: images
    persistentVolumeClaim:
      claimName: {{ include "tokenhub.fullname" . }}-images
  {{- else if eq .Values.imageStorage.type "existingClaim" }}
  - name: images
    persistentVolumeClaim:
      claimName: {{ required "imageStorage.existingClaim is required" .Values.imageStorage.existingClaim }}
  {{- end }}
{{ end }}

export function modelEndpointProtocols(metadata?: Record<string, string>) {
	return (metadata?.endpoints ?? "")
		.split(",")
		.map((endpoint) => endpoint.trim())
		.filter(Boolean);
}

export function modelMetadataFacts(metadata?: Record<string, string>, capabilities: string[] = [], supportedParameters: string[] = []) {
	const facts: Array<{ kind: "protocols" | "parameters" | "capabilities"; values: string[] }> = [];
	const protocols = modelEndpointProtocols(metadata);
	if (protocols.length > 0) facts.push({ kind: "protocols", values: protocols });
	if (supportedParameters.length > 0) facts.push({ kind: "parameters", values: supportedParameters });
	if (capabilities.length > 0) facts.push({ kind: "capabilities", values: capabilities });
	return facts;
}

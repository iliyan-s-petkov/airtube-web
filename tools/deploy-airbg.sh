#!/bin/sh
# Deploy airbg.org to the Proxmox guest. See docs/backlog/ for what the role does.
#
# The Infisical credentials are read from the login keychain and exported here
# rather than in the caller's command line, so they never reach a transcript,
# a shell history or an agent's context.
set -eu

playbook=automations/ansible_collections/home/apps/playbooks/airbg.yml
cd "${AIRBG_AUTOMATION_DIR:-$HOME/Work/home/automation/ansible}"

for pair in INFISICAL_CLIENT_ID:infisical-id \
	INFISICAL_CLIENT_SECRET:infisical-secret \
	INFISICAL_PROJECT_ID:infisical-project; do
	var=${pair%%:*}
	svc=${pair#*:}
	val=$(security find-generic-password -s "$svc" -w) || {
		echo "keychain item '$svc' not readable; unlock the login keychain" >&2
		exit 1
	}
	export "$var=$val"
done

exec ansible-playbook "$playbook" "$@"

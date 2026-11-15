#!/bin/sh

# Confirms that /models contains only the approved model pinned in
# approved-model.env.
#
# Exit status: 0 verified, 10 not provisioned yet, 20 unapproved or modified.
#
# The check runs in the job runner so the manifest is decoded as JSON, the
# same way Ollama reads it, rather than searched as text. Its entries must
# match the pinned manifest exactly, and every referenced file is hashed.

exec /usr/local/bin/ollama-job verify-model

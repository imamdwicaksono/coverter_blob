#!/bin/bash

# Pastikan GitHub Copilot CLI sudah terinstal dan login
# Install: npm install -g @githubnext/github-copilot-cli
# Login: github-copilot-cli auth login

# Tarik update terbaru
git pull

# Tambahkan semua perubahan
git add .

# Dapatkan pesan commit dari Copilot CLI
COMMIT_MSG=$(github-copilot-cli suggest-commit-message)

# Jika pesan kosong, gunakan default
if [ -z "$COMMIT_MSG" ]; then
  COMMIT_MSG="update"
fi

# Commit
git commit -m "$COMMIT_MSG"

# Push ke remote
git push

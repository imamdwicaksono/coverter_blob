if [[ "$(uname)" == "Linux" ]]; then
    ./converter_linux --env .env --extract
else
    go run . --env .env --extract
fi
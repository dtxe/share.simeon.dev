# extractbench corpus

Real receipt photos for `cmd/extractbench` to run strategies against. The
`.jpg`/`.jpeg` files themselves are gitignored (real receipts, don't want
them in a public repo history) — populate this directory locally:

1. Copy saved receipt photos here (`*.jpg` / `*.jpeg`), prioritizing ones
   you've already seen MiniMax misread.
2. Optionally hand-enter ground truth for the tricky ones in
   `expected.json`, keyed by filename:

   ```json
   {
     "receipt1.jpg": { "subtotalCents": 4523 }
   }
   ```

3. Run: `docker compose exec backend go run ./cmd/extractbench -strategy=baseline -dir=testdata/receipts`

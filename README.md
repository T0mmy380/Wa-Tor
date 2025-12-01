## Performance Results

The Wa-Tor simulation was benchmarked using 1, 2, 4 and 8 threads
over 100, 500 and 1000 simulation steps. All timings shown are
median values across 3 runs for each configuration.

Although multiple threads were used, the simulation does not
benefit from parallelism. In fact, performance is slower at higher
thread counts due to excessive synchronization and shared-memory
contention in the tile-based locking system.

### Key Findings
* Best performance achieved using 1 thread
* Speedup drops below 1.0 for 2–8 threads
* 8 threads was consistently the slowest

This shows that the current design is not parallel-scalable.
Further optimization would require reducing mutex usage and
minimizing shared memory interactions.

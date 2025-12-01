package main

import (
	"fmt"
	"image/color"
	"log"
	"math"
	"math/rand"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/hajimehoshi/ebiten"
)

// Simulation settings
var numShark int = 4000
var numFish int = 10000
var fishBreed int = 3   // ticks before fish can breed
var sharkBreed int = 8  // ticks before shark can breed
var sharkStarve int = 3 // ticks a shark can go without eating
var threads int = 1     // number of worker goroutines

const width = 400
const height = 400

// Grid values: 0 empty, 1 fish, 2 shark
var grid [width][height]uint8 = [width][height]uint8{}
var buffer [width][height]uint8 = [width][height]uint8{}

// Breed timers (per cell)
var breedTimer [width][height]int
var bufferBreed [width][height]int

// Shark starvation timers (per cell)
var starveTimer [width][height]int
var bufferStarve [width][height]int

const scale int = 1

var bg color.Color = color.RGBA{69, 145, 196, 255}
var fish color.Color = color.RGBA{255, 230, 120, 255}
var shark color.Color = color.RGBA{200, 50, 50, 255}

var count int = 0

// countFish returns current fish count.
func countFish() int {
	cnt := 0
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			if grid[x][y] == 1 {
				cnt++
			}
		}
	}
	return cnt
}

// update computes the next simulation tick.
// Uses a buffer to build the next state while reading from `grid`.
// The grid is split across goroutines; each cell is processed independently.
func update() error {
	// Clear next-state buffers
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			buffer[x][y] = 0
			bufferBreed[x][y] = 0
			bufferStarve[x][y] = 0
		}
	}

	var wg sync.WaitGroup

	innerWidth := width
	if threads > innerWidth {
		threads = innerWidth
	}

	// Choose a tile grid close to a square of `threads` workers.
	tileCols := int(math.Sqrt(float64(threads)))
	if tileCols <= 0 {
		tileCols = 1
	}
	tileRows := (threads + tileCols - 1) / tileCols
	if tileRows <= 0 {
		tileRows = 1
	}

	tileW := (width + tileCols - 1) / tileCols
	tileH := (height + tileRows - 1) / tileRows

	// per-tile mutexes to protect writes into buffer/breed/starve
	tileMutex := make([][]sync.Mutex, tileCols)
	for i := 0; i < tileCols; i++ {
		tileMutex[i] = make([]sync.Mutex, tileRows)
	}

	// helpers to lock/unlock either one tile or two tiles in deterministic order
	lockTwo := func(ax, ay, bx, by int) {
		aID := ax*tileRows + ay
		bID := bx*tileRows + by
		if aID == bID {
			tileMutex[ax][ay].Lock()
			return
		}
		if aID < bID {
			tileMutex[ax][ay].Lock()
			tileMutex[bx][by].Lock()
		} else {
			tileMutex[bx][by].Lock()
			tileMutex[ax][ay].Lock()
		}
	}
	unlockTwo := func(ax, ay, bx, by int) {
		aID := ax*tileRows + ay
		bID := bx*tileRows + by
		if aID == bID {
			tileMutex[ax][ay].Unlock()
			return
		}
		if aID < bID {
			tileMutex[bx][by].Unlock()
			tileMutex[ax][ay].Unlock()
		} else {
			tileMutex[ax][ay].Unlock()
			tileMutex[bx][by].Unlock()
		}
	}

	// Launch one goroutine per tile (or group tiles to match threads)
	for tx := 0; tx < tileCols; tx++ {
		for ty := 0; ty < tileRows; ty++ {
			startX := tx * tileW
			endX := startX + tileW
			if endX > width {
				endX = width
			}
			startY := ty * tileH
			endY := startY + tileH
			if endY > height {
				endY = height
			}
			// Skip empty tiles
			if startX >= endX || startY >= endY {
				continue
			}

			wg.Add(1)
			go func(sx, ex, sy, ey, ttx, tty int) {
				defer wg.Done()

				for x := sx; x < ex; x++ {
					for y := sy; y < ey; y++ {
						// Fish behavior
						if grid[x][y] == 1 {
							directions := [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}}
							rand.Shuffle(len(directions), func(i, j int) {
								directions[i], directions[j] = directions[j], directions[i]
							})

							moved := false
							newBreed := breedTimer[x][y] - 1

							for _, dir := range directions {
								nx := (x + dir[0] + width) % width
								ny := (y + dir[1] + height) % height

								ox := nx / tileW
								oy := ny / tileH
								sOx := x / tileW
								sOy := y / tileH

								// lock target tile and source tile (deterministic order)
								lockTwo(sOx, sOy, ox, oy)

								if grid[nx][ny] == 0 && buffer[nx][ny] == 0 {
									if newBreed <= 0 {
										// breed: leave offspring and reset parent timer
										if buffer[x][y] == 0 {
											buffer[x][y] = 1
											bufferBreed[x][y] = fishBreed
										}
										buffer[nx][ny] = 1
										bufferBreed[nx][ny] = fishBreed
									} else {
										// move with decremented timer
										buffer[nx][ny] = 1
										bufferBreed[nx][ny] = newBreed
									}
									moved = true
								}

								unlockTwo(sOx, sOy, ox, oy)

								if moved {
									break
								}
							}

							if !moved {
								sOx := x / tileW
								sOy := y / tileH
								// lock only source tile to write stay-in-place
								tileMutex[sOx][sOy].Lock()
								if buffer[x][y] == 0 {
									buffer[x][y] = 1
									if newBreed < 0 {
										newBreed = 0
									}
									bufferBreed[x][y] = newBreed
								}
								tileMutex[sOx][sOy].Unlock()
							}

							// Shark behavior
						} else if grid[x][y] == 2 {
							directions := [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}}
							rand.Shuffle(len(directions), func(i, j int) {
								directions[i], directions[j] = directions[j], directions[i]
							})

							moved := false
							newBreed := breedTimer[x][y] - 1
							newStarve := starveTimer[x][y] - 1

							// Try to eat a fish first
							for _, dir := range directions {
								nx := (x + dir[0] + width) % width
								ny := (y + dir[1] + height) % height

								ox := nx / tileW
								oy := ny / tileH
								sOx := x / tileW
								sOy := y / tileH

								// lock source and target tiles
								lockTwo(sOx, sOy, ox, oy)

								if grid[nx][ny] == 1 && buffer[nx][ny] == 0 {
									// eat: reset starvation and clear eaten fish
									newStarve = sharkStarve
									// mark eaten fish in original grid (reading other goroutines still read original grid)
									grid[nx][ny] = 0

									if newBreed <= 0 {
										if buffer[x][y] == 0 {
											buffer[x][y] = 2
											bufferBreed[x][y] = sharkBreed
											bufferStarve[x][y] = sharkStarve
										}
										buffer[nx][ny] = 2
										bufferBreed[nx][ny] = sharkBreed
										bufferStarve[nx][ny] = newStarve
									} else {
										buffer[nx][ny] = 2
										bufferBreed[nx][ny] = newBreed
										bufferStarve[nx][ny] = newStarve
									}
									moved = true
								}

								unlockTwo(sOx, sOy, ox, oy)

								if moved {
									break
								}
							}

							// If no fish eaten, try empty neighbor
							if !moved {
								for _, dir := range directions {
									nx := (x + dir[0] + width) % width
									ny := (y + dir[1] + height) % height

									ox := nx / tileW
									oy := ny / tileH
									sOx := x / tileW
									sOy := y / tileH

									lockTwo(sOx, sOy, ox, oy)

									if grid[nx][ny] == 0 && buffer[nx][ny] == 0 {
										// if starved, shark dies (do not write)
										if newStarve <= 0 {
											moved = true
											// nothing to write
										} else if newBreed <= 0 {
											// breed: leave newborn and reset parent
											if buffer[x][y] == 0 {
												buffer[x][y] = 2
												bufferBreed[x][y] = sharkBreed
												bufferStarve[x][y] = sharkStarve
											}
											buffer[nx][ny] = 2
											bufferBreed[nx][ny] = sharkBreed
											bufferStarve[nx][ny] = newStarve
										} else {
											// normal move
											buffer[nx][ny] = 2
											bufferBreed[nx][ny] = newBreed
											bufferStarve[nx][ny] = newStarve
										}
										moved = true
									}

									unlockTwo(sOx, sOy, ox, oy)

									if moved {
										break
									}
								}
							}

							if !moved {
								sOx := x / tileW
								sOy := y / tileH
								// stay or die if starved
								if newStarve <= 0 {
									// die
								} else {
									tileMutex[sOx][sOy].Lock()
									if buffer[x][y] == 0 {
										buffer[x][y] = 2
										if newBreed < 0 {
											newBreed = 0
										}
										bufferBreed[x][y] = newBreed
										bufferStarve[x][y] = newStarve
									}
									tileMutex[sOx][sOy].Unlock()
								}
							}
						}
					}
				}
			}(startX, endX, startY, endY, tx, ty)
		}
	}

	wg.Wait()

	// Swap grids and timer arrays (copy assignment)
	temp := buffer
	buffer = grid
	grid = temp

	tempBreed := bufferBreed
	bufferBreed = breedTimer
	breedTimer = tempBreed

	tempStarve := bufferStarve
	bufferStarve = starveTimer
	starveTimer = tempStarve

	//fmt.Printf("Fish: %d\n", countFish())

	return nil
}

// display renders `grid` to the Ebiten image.
func display(window *ebiten.Image) {
	window.Fill(bg)

	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			for i := 0; i < scale; i++ {
				for j := 0; j < scale; j++ {
					switch grid[x][y] {
					case 1:
						window.Set(x*scale+i, y*scale+j, fish)
					case 2:
						window.Set(x*scale+i, y*scale+j, shark)
					}
				}
			}
		}
	}
}

// frame runs each frame; update is called intermittently (controlled by `count`).
func frame(window *ebiten.Image) error {
	count++
	var err error = nil
	if count == 1 {
		err = update()
		count = 0
	}
	if !ebiten.IsDrawingSkipped() {
		display(window)
	}

	return err
}

func initWorld() {
	// Clear everything
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			grid[x][y] = 0
			breedTimer[x][y] = 0
			starveTimer[x][y] = 0
		}
	}

	// Place initial fish
	for i := 0; i < numFish; i++ {
		x := rand.Intn(width)
		y := rand.Intn(height)
		if grid[x][y] == 0 {
			grid[x][y] = 1
			breedTimer[x][y] = fishBreed
		} else {
			i--
		}
	}

	// Place initial sharks
	for i := 0; i < numShark; i++ {
		x := rand.Intn(width)
		y := rand.Intn(height)
		if grid[x][y] == 0 {
			grid[x][y] = 2
			breedTimer[x][y] = sharkBreed
			starveTimer[x][y] = sharkStarve
		} else {
			i--
		}
	}
}

func runSingleBenchmark(steps int, thr int) time.Duration {
	threads = thr
	runtime.GOMAXPROCS(threads)

	// fixed seed so all runs start with same initial world
	rand.Seed(42)
	initWorld()

	start := time.Now()
	for i := 0; i < steps; i++ {
		update()
	}
	elapsed := time.Since(start)

	return elapsed
}

func runBenchmarks() {
	steps := 1000 // or 500 / 1000, just keep it consistent across runs

	threadConfigs := []int{1, 2, 4, 8}
	fmt.Printf("threads,steps,time_seconds\n")
	for _, thr := range threadConfigs {
		dur := runSingleBenchmark(steps, thr)
		seconds := dur.Seconds()
		fmt.Printf("%d,%d,%.6f\n", thr, steps, seconds)
	}
}

func main() {
	rand.Seed(time.Now().UnixNano())

	// Simple arg check: if first arg is "bench", run benchmark mode
	if len(os.Args) > 1 && os.Args[1] == "bench" {
		runBenchmarks()
		return
	}

	// ==== normal graphical mode ====
	runtime.GOMAXPROCS(threads)

	initWorld()
	fmt.Printf("Initial fish: %d\n", countFish())

	if err := ebiten.Run(frame, width, height, 2, "Wa-Tor"); err != nil {
		log.Fatal(err)
	}
}

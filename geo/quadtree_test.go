package geo

import (
	"strings"
	"testing"
)

type point struct{ x, y float64 }

func (p point) X() float64 { return p.x }
func (p point) Y() float64 { return p.y }

func TestQuadTree(t *testing.T) {
	t.Run("not configured", func(t *testing.T) {
		q := QuadTree{}
		if err := q.Insert(point{}); err == nil || !strings.Contains(err.Error(), "must be instantiated") {
			t.Fatal("should error on insert if instantiated incorrectly", err)
		}
	})

	t.Run("out of range", func(t *testing.T) {
		q := QuadTree{Config: Config{10, 10}, Area: Area{-10, -10, 10, 10}}
		if err := q.Insert(point{-20, -20}); err == nil || !strings.Contains(err.Error(), "does not contain") {
			t.Errorf("should error on insert if point is outside of quad tree")
			return
		}
	})

	t.Run("find", func(t *testing.T) {
		q, count := QuadTree{Config: Config{10, 10}, Area: Area{-10, -10, 10, 10}}, 0
		for x := -10.0; x < 10; x++ {
			for y := -10.0; y < 10; y++ {
				count++
				if err := q.Insert(point{x + 0.1, y + 0.1}); err != nil {
					t.Error(err)
					return
				}
			}
		}
		// all
		if ps := q.Find(&q.Area); len(ps) != count {
			t.Errorf("should return the %d inserted points, instead found %d", count, len(ps))
		}
		// sub area
		if ps := q.Find(&Area{-10, -10, 0, 0}); len(ps) != count/4 {
			t.Errorf("should return the %d inserted points, instead found %d", count/4, len(ps))
		}
	})
}

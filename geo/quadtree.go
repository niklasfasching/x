package geo

import (
	"fmt"
)

type QuadTree[P Point] struct {
	Area
	Config
	Points    []P
	Quadrants []*QuadTree[P]
	Lvl       int
}

type Config struct{ MaxLvl, MaxPoints int }

type Area struct{ SWx, SWy, NEx, NEy float64 }

type Point interface {
	X() float64
	Y() float64
}

func (a *Area) Contains(p Point) bool {
	return p.X() >= a.SWx && p.X() <= a.NEx && p.Y() >= a.SWy && p.Y() <= a.NEy
}

func (a *Area) Intersects(b *Area) bool {
	return a.SWx < b.NEx && a.NEx > b.SWx && a.SWy < b.NEy && a.NEy > b.SWy
}

func (q *QuadTree[P]) Find(a *Area) []P {
	if len(q.Points) > 0 {
		ps := []P{}
		for _, p := range q.Points {
			if a.Contains(p) {
				ps = append(ps, p)
			}
		}
		return ps
	}
	if len(q.Quadrants) == 0 {
		return nil
	}
	ps := []P{}
	for _, q := range q.Quadrants {
		if q.Intersects(a) {
			ps = append(ps, q.Find(a)...)
		}
	}
	return ps
}

func (q *QuadTree[P]) Insert(p P) error {
	if q.Config == (Config{}) || q.Area == (Area{}) {
		return fmt.Errorf("quadtree must be instantiated with config and area")
	} else if !q.Contains(p) {
		return fmt.Errorf("%#v does not contain [%v, %v]", q.Area, p.X(), p.Y())
	}
	return q.insert(p)
}

func (q *QuadTree[P]) insert(p P) error {
	if len(q.Quadrants) == 0 {
		if len(q.Points) < q.MaxPoints || q.Lvl >= q.MaxLvl {
			q.Points = append(q.Points, p)
			return nil
		}
		if err := q.subDivide(); err != nil {
			return err
		}
	}
	for _, q := range q.Quadrants {
		if q.Contains(p) {
			return q.insert(p)
		}
	}
	return fmt.Errorf("%#v does not contain [%v, %v]", q.Area, p.X(), p.Y())
}

func (q *QuadTree[P]) subDivide() error {
	points, dx, dy, config, lvl := q.Points, q.NEx-q.SWx, q.NEy-q.SWy, q.Config, q.Lvl+1
	q.Points, q.Quadrants = nil, []*QuadTree[P]{
		{Area: Area{q.SWx + dx/2, q.SWy + dy/2, q.NEx, q.NEy}, Config: config, Lvl: lvl},
		{Area: Area{q.SWx, q.SWy, q.NEx - dx/2, q.NEy - dy/2}, Config: config, Lvl: lvl},
		{Area: Area{q.SWx + dx/2, q.SWy, q.NEx, q.NEy - dy/2}, Config: config, Lvl: lvl},
		{Area: Area{q.SWx, q.SWy + dy/2, q.NEx - dx/2, q.NEy}, Config: config, Lvl: lvl},
	}
	for _, p := range points {
		if err := q.insert(p); err != nil {
			return err
		}
	}
	return nil
}

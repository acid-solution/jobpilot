package agentcontext

// SummarySource bounds one summary request. Progress through a particularly
// long message is persisted separately; Covered advances only at a completed
// turn boundary, so an interrupted chunk never skips unseen source text.
func SummarySource(old Summary, older []Message, budget int) ([]Message, Summary) {
	next := Summary{Covered: old.Covered, ExpectedCovered: old.ExpectedCovered, PartialSequence: old.PartialSequence, PartialOffset: old.PartialOffset}
	source := []Message{}
	for i, m := range older {
		if m.Sequence < old.PartialSequence {
			continue
		}
		offset := 0
		if m.Sequence == old.PartialSequence {
			offset = old.PartialOffset
		}
		runes := []rune(m.Content)
		if offset > len(runes) {
			return nil, old
		}
		fragment := m
		fragment.Content = string(runes[offset:])
		if Estimate(source)+Estimate(fragment)+Estimate(old.Items) > budget {
			lo, hi := 0, len(runes)-offset
			for lo < hi {
				mid := (lo + hi + 1) / 2
				fragment.Content = string(runes[offset : offset+mid])
				if Estimate(source)+Estimate(fragment)+Estimate(old.Items) <= budget {
					lo = mid
				} else {
					hi = mid - 1
				}
			}
			if lo == 0 {
				break
			}
			fragment.Content = string(runes[offset : offset+lo])
			source = append(source, fragment)
			next.PartialSequence = m.Sequence
			next.PartialOffset = offset + lo
			return source, next
		}
		source = append(source, fragment)
		if i+1 < len(older) && m.TurnID != nil && older[i+1].TurnID != nil && *m.TurnID == *older[i+1].TurnID {
			next.PartialSequence = older[i+1].Sequence
			next.PartialOffset = 0
		} else {
			next.Covered = m.Sequence
			next.PartialSequence = 0
			next.PartialOffset = 0
		}
	}
	return source, next
}

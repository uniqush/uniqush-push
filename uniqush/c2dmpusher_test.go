/*
 *  Uniqush by Nan Deng
 *  Copyright (C) 2010 Nan Deng
 *
 *  This software is free software; you can redistribute it and/or
 *  modify it under the terms of the GNU Lesser General Public
 *  License as published by the Free Software Foundation; either
 *  version 3.0 of the License, or (at your option) any later version.
 *
 *  This software is distributed in the hope that it will be useful,
 *  but WITHOUT ANY WARRANTY; without even the implied warranty of
 *  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the GNU
 *  Lesser General Public License for more details.
 *
 *  You should have received a copy of the GNU Lesser General Public
 *  License along with this software; if not, write to the Free Software
 *  Foundation, Inc., 59 Temple Place, Suite 330, Boston, MA  02111-1307  USA
 *
 *  Nan Deng <monnand@gmail.com>
 *
 */

package uniqush

import (
    "testing"
)


const (
    registration_id string = "APA91bHSvnhxU_VDRTnNh2m1TqZUBGC9rvOdb71Zz2aD4VX0gALsNXkd2N0G9nrUgvDtnERdxNi3WNyFJXSFr_OlaJYqyLENLAzw8zOxFv_yvNg2kjwdQJ0"
    auth string = "DQAAALkAAAAeqF537EgIS48gts0EiLBZN8GCzoPuBgfWhBYHCErbLQcLRtaKKqn9mSjjXbyBL8K6cZ1TFnMtg5fsXaIOTic1D262M9revomDaeHeOdt-rXA4ps7Dg98PkZWuWEPxT-n3K_n466llh14l6dMLaUZTNxY3tXTBsSIYJc2Fa7LkogNUNrESGwFKgKY6R0ZjjiuNCDQRm8lLt6nnMmXygiKsr5yykD4Npi15TG9d3CqdoWAS54xHoSNsojajs7Wv4ng"
)

func TestC2DMPush(t *testing.T) {
    sp := NewC2DMServiceProvider("provider", "monnand@gmail.com", auth)
    s := NewAndroidDeliveryPoint("sub", "nan.deng.osu@gmail.com", registration_id)
    data := make(map[string]string, 10)
    n := NewNotification("Hello from go client. pkg uniqush. c2dmpusher", data)
    p := NewC2DMPusher()
    id, err := p.Push(sp, s, n)
    if err != nil {
        t.Errorf("Error: %s\n", err)
        return
    }
    t.Log("Successfully send notification, id=", id, "\n")
}

